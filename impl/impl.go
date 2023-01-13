package impl

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hannahhoward/go-pubsub"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	logging "github.com/ipfs/go-log/v2"
	"github.com/ipld/go-ipld-prime/datamodel"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/libp2p/go-libp2p/core/peer"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/xerrors"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/channelmonitor"
	"github.com/filecoin-project/go-data-transfer/v2/channels"
	"github.com/filecoin-project/go-data-transfer/v2/channelsubscriptions"
	"github.com/filecoin-project/go-data-transfer/v2/message"
	"github.com/filecoin-project/go-data-transfer/v2/message/types"
	"github.com/filecoin-project/go-data-transfer/v2/network"
	"github.com/filecoin-project/go-data-transfer/v2/registry"
	"github.com/filecoin-project/go-data-transfer/v2/tracing"
)

var log = logging.Logger("dt-impl")
var cancelSendTimeout = 30 * time.Second

type manager struct {
	dataTransferNetwork  network.DataTransferNetwork
	validatedTypes       *registry.Registry
	transportConfigurers *registry.Registry
	pubSub               *pubsub.PubSub
	readySub             *pubsub.PubSub
	channels             *channels.Channels
	peerID               peer.ID
	transport            datatransfer.Transport
	channelMonitor       *channelmonitor.Monitor
	channelMonitorCfg    *channelmonitor.Config
	transferIDGen        *timeCounter
	spansIndex           *tracing.SpansIndex
	channelSubscriptions *channelsubscriptions.ChannelSubscriptions
}

type internalEvent struct {
	evt   datatransfer.Event
	state datatransfer.ChannelState
}

func dispatcher(evt pubsub.Event, subscriberFn pubsub.SubscriberFn) error {
	ie, ok := evt.(internalEvent)
	if !ok {
		return errors.New("wrong type of event")
	}
	cb, ok := subscriberFn.(datatransfer.Subscriber)
	if !ok {
		return errors.New("wrong type of subscriber")
	}
	cb(ie.evt, ie.state)
	return nil
}

func readyDispatcher(evt pubsub.Event, fn pubsub.SubscriberFn) error {
	migrateErr, ok := evt.(error)
	if !ok && evt != nil {
		return errors.New("wrong type of event")
	}
	cb, ok := fn.(datatransfer.ReadyFunc)
	if !ok {
		return errors.New("wrong type of event subscriber")
	}
	cb(migrateErr)
	return nil
}

// DataTransferOption configures the data transfer manager
type DataTransferOption func(*manager)

// ChannelRestartConfig sets the configuration options for automatically
// restarting push and pull channels
func ChannelRestartConfig(cfg channelmonitor.Config) DataTransferOption {
	return func(m *manager) {
		m.channelMonitorCfg = &cfg
	}
}

// NewDataTransfer initializes a new instance of a data transfer manager
func NewDataTransfer(ds datastore.Batching, dataTransferNetwork network.DataTransferNetwork, transport datatransfer.Transport, options ...DataTransferOption) (datatransfer.Manager, error) {
	m := &manager{
		dataTransferNetwork:  dataTransferNetwork,
		validatedTypes:       registry.NewRegistry(),
		transportConfigurers: registry.NewRegistry(),
		pubSub:               pubsub.New(dispatcher),
		readySub:             pubsub.New(readyDispatcher),
		peerID:               dataTransferNetwork.ID(),
		transport:            transport,
		transferIDGen:        newTimeCounter(),
		spansIndex:           tracing.NewSpansIndex(),
	}

	channels, err := channels.New(ds, m.notifier, &channelEnvironment{m}, dataTransferNetwork.ID())
	if err != nil {
		return nil, err
	}
	m.channels = channels

	// Apply config options
	for _, option := range options {
		option(m)
	}

	// Create push / pull channel monitor after applying config options as the config
	// options may apply to the monitor
	m.channelMonitor = channelmonitor.NewMonitor(m, m.channelMonitorCfg)
	m.channelSubscriptions = channelsubscriptions.NewChannelSubscriptions(m)
	return m, nil
}

func (m *manager) notifier(evt datatransfer.Event, chst datatransfer.ChannelState) {
	err := m.pubSub.Publish(internalEvent{evt, chst})
	if err != nil {
		log.Warnf("err publishing DT event: %s", err.Error())
	}
}

// Start initializes data transfer processing
func (m *manager) Start(ctx context.Context) error {
	log.Info("start data-transfer module")

	go func() {
		err := m.channels.Start(ctx)
		if err != nil {
			log.Errorf("Migrating data transfer state machines: %s", err.Error())
		}
		err = m.readySub.Publish(err)
		if err != nil {
			log.Warnf("Publish data transfer ready event: %s", err.Error())
		}
	}()

	dtReceiver := &receiver{m}
	m.dataTransferNetwork.SetDelegate(dtReceiver)
	return m.transport.SetEventHandler(m)
}

// OnReady registers a listener for when the data transfer manager has finished starting up
func (m *manager) OnReady(ready datatransfer.ReadyFunc) {
	m.readySub.Subscribe(ready)
}

// Stop terminates all data transfers and ends processing
func (m *manager) Stop(ctx context.Context) error {
	log.Info("stop data-transfer module")
	m.channelMonitor.Shutdown()
	m.spansIndex.EndAll()
	m.channelSubscriptions.Stop()
	return m.transport.Shutdown(ctx)
}

// RegisterVoucherType registers a validator for the given voucher type
// returns error if:
// * voucher type does not implement voucher
// * there is a voucher type registered with an identical identifier
// * voucherType's Kind is not reflect.Ptr
func (m *manager) RegisterVoucherType(voucherType datatransfer.TypeIdentifier, validator datatransfer.RequestValidator) error {
	err := m.validatedTypes.Register(voucherType, validator)
	if err != nil {
		return xerrors.Errorf("error registering voucher type: %w", err)
	}
	return nil
}

// OpenPushDataChannel opens a data transfer that will send data to the recipient peer and
// transfer parts of the piece that match the selector
func (m *manager) OpenPushDataChannel(ctx context.Context, requestTo peer.ID, voucher datatransfer.TypedVoucher, baseCid cid.Cid, selector datamodel.Node, options ...datatransfer.TransferOption) (datatransfer.ChannelID, error) {
	log.Infof("open push channel to %s with base cid %s", requestTo, baseCid)

	tc := datatransfer.FromOptions(options)

	req, err := m.newRequest(ctx, selector, false, voucher, baseCid, requestTo)
	if err != nil {
		return datatransfer.ChannelID{}, err
	}

	chid, err := m.channels.CreateNew(m.peerID, req.TransferID(), baseCid, selector, voucher,
		m.peerID, m.peerID, requestTo) // initiator = us, sender = us, receiver = them
	if err != nil {
		return chid, err
	}

	if eventsCb := tc.EventsCb(); eventsCb != nil {
		m.channelSubscriptions.Subscribe(chid, eventsCb)
	}

	if err := m.channels.Open(chid); err != nil {
		return chid, err
	}

	ctx, span := m.spansIndex.SpanForChannel(ctx, chid)
	processor, has := m.transportConfigurers.Processor(voucher.Type)
	if has {
		transportConfigurer := processor.(datatransfer.TransportConfigurer)
		transportConfigurer(chid, voucher, m.transport)
	}
	m.dataTransferNetwork.Protect(requestTo, chid.String())
	monitoredChan := m.channelMonitor.AddPushChannel(chid)
	if err := m.dataTransferNetwork.SendMessage(ctx, requestTo, req); err != nil {
		err = fmt.Errorf("unable to send request: %w", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		_ = m.channels.Error(chid, err)

		// If push channel monitoring is enabled, shutdown the monitor as it
		// wasn't possible to start the data transfer
		if monitoredChan != nil {
			monitoredChan.Shutdown()
		}

		return chid, err
	}

	return chid, nil
}

// OpenPullDataChannel opens a data transfer that will request data from the sending peer and
// transfer parts of the piece that match the selector
func (m *manager) OpenPullDataChannel(ctx context.Context, requestTo peer.ID, voucher datatransfer.TypedVoucher, baseCid cid.Cid, selector datamodel.Node, options ...datatransfer.TransferOption) (datatransfer.ChannelID, error) {
	log.Infof("open pull channel to %s with base cid %s", requestTo, baseCid)

	tc := datatransfer.FromOptions(options)

	req, err := m.newRequest(ctx, selector, true, voucher, baseCid, requestTo)
	if err != nil {
		return datatransfer.ChannelID{}, err
	}

	// initiator = us, sender = them, receiver = us
	chid, err := m.channels.CreateNew(m.peerID, req.TransferID(), baseCid, selector, voucher,
		m.peerID, requestTo, m.peerID)
	if err != nil {
		return chid, err
	}

	if eventsCb := tc.EventsCb(); eventsCb != nil {
		m.channelSubscriptions.Subscribe(chid, eventsCb)
	}

	if err := m.channels.Open(chid); err != nil {
		return chid, err
	}

	ctx, span := m.spansIndex.SpanForChannel(ctx, chid)
	processor, has := m.transportConfigurers.Processor(voucher.Type)
	if has {
		transportConfigurer := processor.(datatransfer.TransportConfigurer)
		transportConfigurer(chid, voucher, m.transport)
	}
	m.dataTransferNetwork.Protect(requestTo, chid.String())
	monitoredChan := m.channelMonitor.AddPullChannel(chid)
	if err := m.transport.OpenChannel(ctx, requestTo, chid, cidlink.Link{Cid: baseCid}, selector, nil, req); err != nil {
		err = fmt.Errorf("unable to send request: %w", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		_ = m.channels.Error(chid, err)

		// If pull channel monitoring is enabled, shutdown the monitor as it
		// wasn't possible to start the data transfer
		if monitoredChan != nil {
			monitoredChan.Shutdown()
		}
		return chid, err
	}
	return chid, nil
}

// SendVoucher sends an intermediate voucher as needed when the receiver sends a request for revalidation
func (m *manager) SendVoucher(ctx context.Context, channelID datatransfer.ChannelID, voucher datatransfer.TypedVoucher) error {
	chst, err := m.channels.GetByID(ctx, channelID)
	if err != nil {
		return err
	}
	ctx, _ = m.spansIndex.SpanForChannel(ctx, channelID)
	ctx, span := otel.Tracer("data-transfer").Start(ctx, "sendVoucher", trace.WithAttributes(
		attribute.String("channelID", channelID.String()),
	))
	defer span.End()
	if channelID.Initiator != m.peerID {
		err := errors.New("cannot send voucher for request we did not initiate")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	updateRequest, err := message.VoucherRequest(channelID.ID, &voucher)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	if err := m.dataTransferNetwork.SendMessage(ctx, chst.OtherPeer(), updateRequest); err != nil {
		err = fmt.Errorf("unable to send request: %w", err)
		_ = m.OnRequestDisconnected(channelID, err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	return m.channels.NewVoucher(channelID, voucher)
}

func (m *manager) SendVoucherResult(ctx context.Context, channelID datatransfer.ChannelID, voucherResult datatransfer.TypedVoucher) error {
	chst, err := m.channels.GetByID(ctx, channelID)
	if err != nil {
		return err
	}
	ctx, _ = m.spansIndex.SpanForChannel(ctx, channelID)
	ctx, span := otel.Tracer("data-transfer").Start(ctx, "sendVoucherResult", trace.WithAttributes(
		attribute.String("channelID", channelID.String()),
	))
	defer span.End()
	if channelID.Initiator == m.peerID {
		err := errors.New("cannot send voucher result for request we initiated")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	var updateResponse datatransfer.Response
	if chst.Status().InFinalization() {
		updateResponse, err = message.CompleteResponse(channelID.ID, chst.Status().IsAccepted(), chst.ResponderPaused(), &voucherResult)
	} else {
		updateResponse, err = message.VoucherResultResponse(channelID.ID, chst.Status().IsAccepted(), chst.ResponderPaused(), &voucherResult)
	}

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	if err := m.dataTransferNetwork.SendMessage(ctx, chst.OtherPeer(), updateResponse); err != nil {
		err = fmt.Errorf("unable to send request: %w", err)
		_ = m.OnRequestDisconnected(channelID, err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	return m.channels.NewVoucherResult(channelID, voucherResult)
}

func (m *manager) UpdateValidationStatus(ctx context.Context, chid datatransfer.ChannelID, result datatransfer.ValidationResult) error {
	ctx, _ = m.spansIndex.SpanForChannel(ctx, chid)
	ctx, span := otel.Tracer("data-transfer").Start(ctx, "updateValidationStatus", trace.WithAttributes(
		attribute.String("channelID", chid.String()),
	))
	err := m.updateValidationStatus(ctx, chid, result)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
	return err
}

// updateValidationStatus is the implementation of the public method, which wraps this private method
// in a trace
func (m *manager) updateValidationStatus(ctx context.Context, chid datatransfer.ChannelID, result datatransfer.ValidationResult) error {
	// first check if we are the responder -- only the responder can call UpdateValidationStatus
	if chid.Initiator == m.peerID {
		err := errors.New("cannot send voucher result for request we initiated")
		return err
	}

	// dispatch channel events and generate a response message
	chst, response, err := m.processValidationUpdate(ctx, chid, result)

	// dispatch transport updates
	return m.handleTransportUpdate(ctx, chst, response, result, err)
}

func (m *manager) processValidationUpdate(ctx context.Context, chid datatransfer.ChannelID, result datatransfer.ValidationResult) (datatransfer.ChannelState, datatransfer.Response, error) {

	// read the channel state
	chst, err := m.channels.GetByID(ctx, chid)
	if err != nil {
		return nil, nil, err
	}

	// if the request is now rejected, error the channel
	if !result.Accepted {
		err = m.recordRejectedValidationEvents(chid, result)
	} else {
		err = m.recordAcceptedValidationEvents(chst, result)
	}
	if err != nil {
		return nil, nil, err
	}

	// generate a response message
	messageType := types.VoucherResultMessage
	if chst.Status() == datatransfer.Finalizing {
		messageType = types.CompleteMessage
	}
	response, msgErr := message.ValidationResultResponse(messageType, chst.TransferID(), result, err,
		result.LeaveRequestPaused(chst))
	if msgErr != nil {
		return nil, nil, msgErr
	}

	// return the response message and any errors
	return chst, response, nil
}

// handleTransportUpdate updates the transport based on the validation status and the
// response message
// TODO: the ordering here is a bit sensitive, and the transport should
// be refactored to accept multiple operations at once and order these itself
func (m *manager) handleTransportUpdate(
	ctx context.Context,
	chst datatransfer.ChannelState,
	response datatransfer.Message,
	result datatransfer.ValidationResult,
	resultErr error,
) error {

	pauseRequest := result.LeaveRequestPaused(chst)
	// resume channel as needed, sending the response message immediately and returning
	if resultErr == nil && result.Accepted && !pauseRequest {
		if chst.ResponderPaused() && !chst.Status().InFinalization() {
			return m.transport.(datatransfer.PauseableTransport).ResumeChannel(ctx, response, chst.ChannelID())
		}
	}

	// send a response message
	if response != nil {
		if err := m.dataTransferNetwork.SendMessage(ctx, chst.ChannelID().Initiator, response); err != nil {
			return err
		}
	}

	// close the channel as needed
	if resultErr != nil || !result.Accepted {
		m.transport.CloseChannel(ctx, chst.ChannelID())
		return resultErr
	}

	// pause the channel as needed
	if pauseRequest && !chst.ResponderPaused() && !chst.Status().InFinalization() {
		return m.transport.(datatransfer.PauseableTransport).PauseChannel(ctx, chst.ChannelID())
	}

	return nil
}

// close an open channel (effectively a cancel)
func (m *manager) CloseDataTransferChannel(ctx context.Context, chid datatransfer.ChannelID) error {
	log.Infof("close channel %s", chid)

	chst, err := m.channels.GetByID(ctx, chid)
	if err != nil {
		return err
	}
	ctx, _ = m.spansIndex.SpanForChannel(ctx, chid)
	ctx, span := otel.Tracer("data-transfer").Start(ctx, "closeChannel", trace.WithAttributes(
		attribute.String("channelID", chid.String()),
	))
	defer span.End()
	// Close the channel on the local transport
	err = m.transport.CloseChannel(ctx, chid)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		log.Warnf("unable to close channel %s: %s", chid, err)
	}

	// Send a cancel message to the remote peer async
	go func() {
		sctx, cancel := context.WithTimeout(context.Background(), cancelSendTimeout)
		defer cancel()
		log.Infof("%s: sending cancel channel to %s for channel %s", m.peerID, chst.OtherPeer(), chid)
		err = m.dataTransferNetwork.SendMessage(sctx, chst.OtherPeer(), m.cancelMessage(chid))
		if err != nil {
			err = fmt.Errorf("unable to send cancel message for channel %s to peer %s: %w",
				chid, m.peerID, err)
			_ = m.OnRequestDisconnected(chid, err)
			log.Warn(err)
		}
	}()

	// Fire a cancel event
	fsmerr := m.channels.Cancel(chid)
	if fsmerr != nil {
		return xerrors.Errorf("unable to send cancel to channel FSM: %w", fsmerr)
	}

	return nil
}

// ConnectTo opens a connection to a peer on the data-transfer protocol,
// retrying if necessary
func (m *manager) ConnectTo(ctx context.Context, p peer.ID) error {
	return m.dataTransferNetwork.ConnectWithRetry(ctx, p)
}

// close an open channel and fire an error event
func (m *manager) CloseDataTransferChannelWithError(ctx context.Context, chid datatransfer.ChannelID, cherr error) error {
	log.Infof("close channel %s with error %s", chid, cherr)

	chst, err := m.channels.GetByID(ctx, chid)
	if err != nil {
		return err
	}
	ctx, _ = m.spansIndex.SpanForChannel(ctx, chid)
	ctx, span := otel.Tracer("data-transfer").Start(ctx, "closeChannel", trace.WithAttributes(
		attribute.String("channelID", chid.String()),
	))
	defer span.End()

	// Cancel the channel on the local transport
	err = m.transport.CloseChannel(ctx, chid)
	if err != nil {
		log.Warnf("unable to close channel %s: %s", chid, err)
	}

	// Try to send a cancel message to the remote peer. It's quite likely
	// we aren't able to send the message to the peer because the channel
	// is already in an error state, which is probably because of connection
	// issues, so if we cant send the message just log a warning.
	log.Infof("%s: sending cancel channel to %s for channel %s", m.peerID, chst.OtherPeer(), chid)
	err = m.dataTransferNetwork.SendMessage(ctx, chst.OtherPeer(), m.cancelMessage(chid))
	if err != nil {
		// Just log a warning here because it's important that we fire the
		// error event with the original error so that it doesn't get masked
		// by subsequent errors.
		log.Warnf("unable to send cancel message for channel %s to peer %s: %w",
			chid, m.peerID, err)
	}

	// Fire an error event
	err = m.channels.Error(chid, cherr)
	if err != nil {
		return xerrors.Errorf("unable to send error %s to channel FSM: %w", cherr, err)
	}

	return nil
}

// pause a running data transfer channel
func (m *manager) PauseDataTransferChannel(ctx context.Context, chid datatransfer.ChannelID) error {
	log.Infof("pause channel %s", chid)

	pausable, ok := m.transport.(datatransfer.PauseableTransport)
	if !ok {
		return datatransfer.ErrUnsupported
	}

	ctx, _ = m.spansIndex.SpanForChannel(ctx, chid)

	err := pausable.PauseChannel(ctx, chid)
	if err != nil {
		log.Warnf("Error attempting to pause at transport level: %s", err.Error())
	}

	if err := m.dataTransferNetwork.SendMessage(ctx, chid.OtherParty(m.peerID), m.pauseMessage(chid)); err != nil {
		err = fmt.Errorf("unable to send pause message: %w", err)
		_ = m.OnRequestDisconnected(chid, err)
		return err
	}

	return m.pause(chid)
}

// resume a running data transfer channel
func (m *manager) ResumeDataTransferChannel(ctx context.Context, chid datatransfer.ChannelID) error {
	log.Infof("resume channel %s", chid)

	pausable, ok := m.transport.(datatransfer.PauseableTransport)
	if !ok {
		return datatransfer.ErrUnsupported
	}

	ctx, _ = m.spansIndex.SpanForChannel(ctx, chid)

	err := pausable.ResumeChannel(ctx, m.resumeMessage(chid), chid)
	if err != nil {
		log.Warnf("Error attempting to resume at transport level: %s", err.Error())
	}

	return m.resume(chid)
}

// get channel state
func (m *manager) ChannelState(ctx context.Context, chid datatransfer.ChannelID) (datatransfer.ChannelState, error) {
	return m.channels.GetByID(ctx, chid)
}

// get status of a transfer
func (m *manager) TransferChannelStatus(ctx context.Context, chid datatransfer.ChannelID) datatransfer.Status {
	chst, err := m.channels.GetByID(ctx, chid)
	if err != nil {
		return datatransfer.ChannelNotFoundError
	}
	return chst.Status()
}

// get notified when certain types of events happen
func (m *manager) SubscribeToEvents(subscriber datatransfer.Subscriber) datatransfer.Unsubscribe {
	return datatransfer.Unsubscribe(m.pubSub.Subscribe(subscriber))
}

// get all in progress transfers
func (m *manager) InProgressChannels(ctx context.Context) (map[datatransfer.ChannelID]datatransfer.ChannelState, error) {
	return m.channels.InProgress()
}

// RegisterTransportConfigurer registers the given transport configurer to be run on requests with the given voucher
// type
func (m *manager) RegisterTransportConfigurer(voucherType datatransfer.TypeIdentifier, configurer datatransfer.TransportConfigurer) error {
	err := m.transportConfigurers.Register(voucherType, configurer)
	if err != nil {
		return xerrors.Errorf("error registering transport configurer: %w", err)
	}
	return nil
}

// RestartDataTransferChannel restarts data transfer on the channel with the given channelId
func (m *manager) RestartDataTransferChannel(ctx context.Context, chid datatransfer.ChannelID) error {
	log.Infof("restart channel %s", chid)

	channel, err := m.channels.GetByID(ctx, chid)
	if err != nil {
		return xerrors.Errorf("failed to fetch channel: %w", err)
	}

	// if channel has already been completed, there is nothing to do.
	// TODO We could be in a state where the channel has completed but the corresponding event hasnt fired in the client/provider.
	if channels.IsChannelTerminated(channel.Status()) {
		return nil
	}

	// if channel is is cleanup state, finish it
	if channels.IsChannelCleaningUp(channel.Status()) {
		return m.channels.CompleteCleanupOnRestart(channel.ChannelID())
	}

	ctx, _ = m.spansIndex.SpanForChannel(ctx, chid)
	ctx, span := otel.Tracer("data-transfer").Start(ctx, "restartChannel", trace.WithAttributes(
		attribute.String("channelID", chid.String()),
	))
	defer span.End()
	// initiate restart
	chType := m.channelDataTransferType(channel)
	switch chType {
	case ManagerPeerReceivePush:
		return m.restartManagerPeerReceivePush(ctx, channel)
	case ManagerPeerReceivePull:
		return m.restartManagerPeerReceivePull(ctx, channel)
	case ManagerPeerCreatePull:
		return m.openPullRestartChannel(ctx, channel)
	case ManagerPeerCreatePush:
		return m.openPushRestartChannel(ctx, channel)
	}

	return nil
}

func (m *manager) channelDataTransferType(channel datatransfer.ChannelState) ChannelDataTransferType {
	initiator := channel.ChannelID().Initiator
	if channel.IsPull() {
		// we created a pull channel
		if initiator == m.peerID {
			return ManagerPeerCreatePull
		}

		// we received a pull channel
		return ManagerPeerReceivePull
	}

	// we created a push channel
	if initiator == m.peerID {
		return ManagerPeerCreatePush
	}

	// we received a push channel
	return ManagerPeerReceivePush
}

func (m *manager) PeerID() peer.ID {
	return m.peerID
}
