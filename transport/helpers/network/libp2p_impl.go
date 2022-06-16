package network

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	logging "github.com/ipfs/go-log/v2"
	"github.com/jpillora/backoff"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/network"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-core/protocol"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/message"
)

var log = logging.Logger("data_transfer_network")

// The maximum amount of time to wait to open a stream
const defaultOpenStreamTimeout = 10 * time.Second

// The maximum time to wait for a message to be sent
var defaultSendMessageTimeout = 10 * time.Second

// The max number of attempts to open a stream
const defaultMaxStreamOpenAttempts = 5

// The min backoff time between retries
const defaultMinAttemptDuration = 1 * time.Second

// The max backoff time between retries
const defaultMaxAttemptDuration = 5 * time.Minute

// The multiplier in the backoff time for each retry
const defaultBackoffFactor = 5

var defaultDataTransferProtocols = []protocol.ID{
	ProtocolDataTransfer1_2,
	ProtocolFilDataTransfer1_2,
}

func isLegacyProtocol(protocol protocol.ID) bool {
	return protocol == ProtocolFilDataTransfer1_2
}

// Option is an option for configuring the libp2p storage market network
type Option func(*libp2pDataTransferNetwork)

// DataTransferProtocols OVERWRITES the default libp2p protocols we use for data transfer with the given protocols.
func DataTransferProtocols(protocols []protocol.ID) Option {
	return func(dtnet *libp2pDataTransferNetwork) {
		dtnet.setDataTransferProtocols(protocols)
	}
}

// SendMessageParameters changes the default parameters around sending messages
func SendMessageParameters(openStreamTimeout time.Duration, sendMessageTimeout time.Duration) Option {
	return func(dtnet *libp2pDataTransferNetwork) {
		dtnet.sendMessageTimeout = sendMessageTimeout
		dtnet.openStreamTimeout = openStreamTimeout
	}
}

// RetryParameters changes the default parameters around connection reopening
func RetryParameters(minDuration time.Duration, maxDuration time.Duration, attempts float64, backoffFactor float64) Option {
	return func(dtnet *libp2pDataTransferNetwork) {
		dtnet.maxStreamOpenAttempts = attempts
		dtnet.minAttemptDuration = minDuration
		dtnet.maxAttemptDuration = maxDuration
		dtnet.backoffFactor = backoffFactor
	}
}

// NewFromLibp2pHost returns a GraphSyncNetwork supported by underlying Libp2p host.
func NewFromLibp2pHost(host host.Host, options ...Option) DataTransferNetwork {
	dataTransferNetwork := libp2pDataTransferNetwork{
		host: host,

		openStreamTimeout:     defaultOpenStreamTimeout,
		sendMessageTimeout:    defaultSendMessageTimeout,
		maxStreamOpenAttempts: defaultMaxStreamOpenAttempts,
		minAttemptDuration:    defaultMinAttemptDuration,
		maxAttemptDuration:    defaultMaxAttemptDuration,
		backoffFactor:         defaultBackoffFactor,
		receivers:             make(map[protocol.ID]receiverData),
		transportProtocols:    make(map[datatransfer.TransportID]transportProtocols),
	}
	dataTransferNetwork.setDataTransferProtocols(defaultDataTransferProtocols)

	for _, option := range options {
		option(&dataTransferNetwork)
	}

	return &dataTransferNetwork
}

type transportProtocols struct {
	protocols       []protocol.ID
	protocolStrings []string
}

type receiverData struct {
	ProtocolDescription
	transportID datatransfer.TransportID
	receiver    Receiver
}

// libp2pDataTransferNetwork transforms the libp2p host interface, which sends and receives
// NetMessage objects, into the data transfer network interface.
type libp2pDataTransferNetwork struct {
	host host.Host
	// inbound messages from the network are forwarded to the receiver
	receivers             map[protocol.ID]receiverData
	transportProtocols    map[datatransfer.TransportID]transportProtocols
	openStreamTimeout     time.Duration
	sendMessageTimeout    time.Duration
	maxStreamOpenAttempts float64
	minAttemptDuration    time.Duration
	maxAttemptDuration    time.Duration
	dtProtocols           []protocol.ID
	backoffFactor         float64
}

func (dtnet *libp2pDataTransferNetwork) openStream(ctx context.Context, id peer.ID, protocols ...protocol.ID) (network.Stream, error) {
	b := &backoff.Backoff{
		Min:    dtnet.minAttemptDuration,
		Max:    dtnet.maxAttemptDuration,
		Factor: dtnet.backoffFactor,
		Jitter: true,
	}

	start := time.Now()
	for {
		tctx, cancel := context.WithTimeout(ctx, dtnet.openStreamTimeout)
		defer cancel()

		// will use the first among the given protocols that the remote peer supports
		at := time.Now()
		s, err := dtnet.host.NewStream(tctx, id, protocols...)
		if err == nil {
			nAttempts := b.Attempt() + 1
			if b.Attempt() > 0 {
				log.Debugf("opened stream to %s on attempt %g of %g after %s",
					id, nAttempts, dtnet.maxStreamOpenAttempts, time.Since(start))
			}

			return s, err
		}

		// b.Attempt() starts from zero
		nAttempts := b.Attempt() + 1
		if nAttempts >= dtnet.maxStreamOpenAttempts {
			return nil, fmt.Errorf("exhausted %g attempts but failed to open stream to %s, err: %w", dtnet.maxStreamOpenAttempts, id, err)
		}

		d := b.Duration()
		log.Warnf("failed to open stream to %s on attempt %g of %g after %s, waiting %s to try again, err: %s",
			id, nAttempts, dtnet.maxStreamOpenAttempts, time.Since(at), d, err)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(d):
		}
	}
}

func (dtnet *libp2pDataTransferNetwork) SendMessage(
	ctx context.Context,
	p peer.ID,
	transportID datatransfer.TransportID,
	outgoing datatransfer.Message) error {

	ctx, span := otel.Tracer("data-transfer").Start(ctx, "sendMessage", trace.WithAttributes(
		attribute.String("to", p.String()),
		attribute.Int64("transferID", int64(outgoing.TransferID())),
		attribute.Bool("isRequest", outgoing.IsRequest()),
		attribute.Bool("isNew", outgoing.IsNew()),
		attribute.Bool("isRestart", outgoing.IsRestart()),
		attribute.Bool("isUpdate", outgoing.IsUpdate()),
		attribute.Bool("isCancel", outgoing.IsCancel()),
		attribute.Bool("isPaused", outgoing.IsPaused()),
	))

	defer span.End()

	transportProtocols, ok := dtnet.transportProtocols[transportID]
	if !ok {
		return datatransfer.ErrUnsupported
	}
	s, err := dtnet.openStream(ctx, p, transportProtocols.protocols...)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	receiverData, ok := dtnet.receivers[s.Protocol()]
	if !ok {
		// this shouldn't happen, but let's be careful just in case to avoid a panic
		err := errors.New("no receiver set")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	outgoing, err = outgoing.MessageForVersion(receiverData.MessageVersion)
	if err != nil {
		err = fmt.Errorf("failed to convert message for protocol: %w", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	if err = dtnet.msgToStream(ctx, s, outgoing, receiverData); err != nil {
		if err2 := s.Reset(); err2 != nil {
			log.Error(err)
			span.RecordError(err2)
			span.SetStatus(codes.Error, err2.Error())
			return err2
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	return s.Close()
}

func (dtnet *libp2pDataTransferNetwork) SetDelegate(transportID datatransfer.TransportID, versions []datatransfer.Version, r Receiver) {
	transportProtocols := transportProtocols{}
	for _, dtProtocol := range dtnet.dtProtocols {
		messageVersion, _ := MessageVersion(dtProtocol)
		if isLegacyProtocol(dtProtocol) {
			if transportID == datatransfer.LegacyTransportID {
				supportsLegacyVersion := false
				for _, version := range versions {
					if version == datatransfer.LegacyTransportVersion {
						supportsLegacyVersion = true
						break
					}
				}
				if !supportsLegacyVersion {
					continue
				}
				dtnet.receivers[dtProtocol] = receiverData{
					ProtocolDescription: ProtocolDescription{
						IsLegacy:         true,
						TransportVersion: datatransfer.LegacyTransportVersion,
						MessageVersion:   messageVersion,
					},
					transportID: transportID,
					receiver:    r,
				}
				transportProtocols.protocols = append(transportProtocols.protocols, dtProtocol)
				transportProtocols.protocolStrings = append(transportProtocols.protocolStrings, string(dtProtocol))
			}
		} else {
			for _, version := range versions {
				joinedProtocol := strings.Join([]string{string(dtProtocol), string(transportID), version.String()}, "/")
				dtnet.receivers[protocol.ID(joinedProtocol)] = receiverData{
					ProtocolDescription: ProtocolDescription{
						IsLegacy:         false,
						TransportVersion: version,
						MessageVersion:   messageVersion,
					},
					transportID: transportID,
					receiver:    r,
				}
				transportProtocols.protocols = append(transportProtocols.protocols, protocol.ID(joinedProtocol))
				transportProtocols.protocolStrings = append(transportProtocols.protocolStrings, joinedProtocol)
			}
		}
	}

	dtnet.transportProtocols[transportID] = transportProtocols

	for _, p := range transportProtocols.protocols {
		dtnet.host.SetStreamHandler(p, dtnet.handleNewStream)
	}
}

func (dtnet *libp2pDataTransferNetwork) ConnectTo(ctx context.Context, p peer.ID) error {
	return dtnet.host.Connect(ctx, peer.AddrInfo{ID: p})
}

// ConnectWithRetry establishes a connection to the given peer, retrying if
// necessary, and opens a stream on the data-transfer protocol to verify
// the peer will accept messages on the protocol
func (dtnet *libp2pDataTransferNetwork) ConnectWithRetry(ctx context.Context, p peer.ID, transportID datatransfer.TransportID) error {
	transportProtocols, ok := dtnet.transportProtocols[transportID]
	if !ok {
		return datatransfer.ErrUnsupported
	}
	// Open a stream over the data-transfer protocol, to make sure that the
	// peer is listening on the protocol
	s, err := dtnet.openStream(ctx, p, transportProtocols.protocols...)
	if err != nil {
		return err
	}

	// We don't actually use the stream, we just open it to verify it's
	// possible to connect over the data-transfer protocol, so we close it here
	return s.Close()
}

// handleNewStream receives a new stream from the network.
func (dtnet *libp2pDataTransferNetwork) handleNewStream(s network.Stream) {
	defer s.Close() // nolint: errcheck,gosec

	if len(dtnet.receivers) == 0 {
		s.Reset() // nolint: errcheck,gosec
		return
	}

	receiverData, ok := dtnet.receivers[s.Protocol()]
	if !ok {
		s.Reset() // nolint: errcheck,gosec
		return
	}
	p := s.Conn().RemotePeer()
	// if we have no transport handler, reset the stream
	for {
		var received datatransfer.Message
		var err error
		if receiverData.IsLegacy {
			received, err = message.FromNet(s)
		} else {
			received, err = message.FromNetWrapped(s)
		}

		if err != nil {
			if err != io.EOF && err != io.ErrUnexpectedEOF {
				s.Reset() // nolint: errcheck,gosec
				log.Errorf("net handleNewStream from %s error: %s", p, err)
			}
			return
		}

		ctx := context.Background()
		log.Debugf("net handleNewStream from %s", p)

		if received.IsRequest() {
			receivedRequest, ok := received.(datatransfer.Request)
			if ok {
				if receivedRequest.IsRestartExistingChannelRequest() {
					receiverData.receiver.ReceiveRestartExistingChannelRequest(ctx, p, receivedRequest)
				} else {
					receiverData.receiver.ReceiveRequest(ctx, p, receivedRequest)
				}
			}
		} else {
			receivedResponse, ok := received.(datatransfer.Response)
			if ok {
				receiverData.receiver.ReceiveResponse(ctx, p, receivedResponse)
			}
		}
	}
}

func (dtnet *libp2pDataTransferNetwork) ID() peer.ID {
	return dtnet.host.ID()
}

func (dtnet *libp2pDataTransferNetwork) Protect(id peer.ID, tag string) {
	dtnet.host.ConnManager().Protect(id, tag)
}

func (dtnet *libp2pDataTransferNetwork) Unprotect(id peer.ID, tag string) bool {
	return dtnet.host.ConnManager().Unprotect(id, tag)
}

func (dtnet *libp2pDataTransferNetwork) msgToStream(ctx context.Context, s network.Stream, msg datatransfer.Message, receiverData receiverData) error {
	if msg.IsRequest() {
		log.Debugf("Outgoing request message for transfer ID: %d", msg.TransferID())
	}

	deadline := time.Now().Add(dtnet.sendMessageTimeout)
	if dl, ok := ctx.Deadline(); ok {
		deadline = dl
	}
	if err := s.SetWriteDeadline(deadline); err != nil {
		log.Warnf("error setting deadline: %s", err)
	}
	defer func() {
		if err := s.SetWriteDeadline(time.Time{}); err != nil {
			log.Warnf("error resetting deadline: %s", err)
		}
	}()

	if !receiverData.IsLegacy {
		msg = msg.WrappedForTransport(receiverData.transportID, receiverData.TransportVersion)
	}

	if err := msg.ToNet(s); err != nil {
		log.Debugf("error: %s", err)
		return err
	}
	return nil
}

func (dtnet *libp2pDataTransferNetwork) Protocol(ctx context.Context, id peer.ID, transportID datatransfer.TransportID) (ProtocolDescription, error) {
	transportProtocols, ok := dtnet.transportProtocols[transportID]
	if !ok {
		return ProtocolDescription{}, datatransfer.ErrUnsupported
	}

	// Check the cache for the peer's protocol version
	firstProto, err := dtnet.host.Peerstore().FirstSupportedProtocol(id, transportProtocols.protocolStrings...)
	if err != nil {
		return ProtocolDescription{}, err
	}

	if firstProto != "" {
		receiverData, ok := dtnet.receivers[protocol.ID(firstProto)]
		if !ok {
			return ProtocolDescription{}, err
		}
		return receiverData.ProtocolDescription, nil
	}

	// The peer's protocol version is not in the cache, so connect to the peer.
	// Note that when the stream is opened, the peer's protocol will be added
	// to the cache.
	s, err := dtnet.openStream(ctx, id, dtnet.dtProtocols...)
	if err != nil {
		return ProtocolDescription{}, err
	}
	_ = s.Close()
	receiverData, ok := dtnet.receivers[s.Protocol()]
	if !ok {
		return ProtocolDescription{}, err
	}
	return receiverData.ProtocolDescription, nil
}

func (dtnet *libp2pDataTransferNetwork) setDataTransferProtocols(protocols []protocol.ID) {
	dtnet.dtProtocols = append([]protocol.ID{}, protocols...)
}
