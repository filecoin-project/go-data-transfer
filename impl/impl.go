package impl

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ipfs/go-cid"
	logging "github.com/ipfs/go-log"
	"github.com/ipld/go-ipld-prime"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/peer"
	"golang.org/x/xerrors"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/channels"
	"github.com/filecoin-project/go-data-transfer/message"
	"github.com/filecoin-project/go-data-transfer/network"
	"github.com/filecoin-project/go-data-transfer/registry"
	"github.com/filecoin-project/go-data-transfer/transport"
	"github.com/filecoin-project/go-storedcounter"
	"github.com/hannahhoward/go-pubsub"
)

var log = logging.Logger("dt-impl")

type manager struct {
	dataTransferNetwork network.DataTransferNetwork
	validatedTypes      *registry.Registry
	pubSub              *pubsub.PubSub
	channels            *channels.Channels
	peerID              peer.ID
	transport           transport.Transport
	storedCounter       *storedcounter.StoredCounter
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
		return errors.New("wrong type of event")
	}
	cb(ie.evt, ie.state)
	return nil
}

// NewDataTransfer initializes a new instance of a data transfer manager
func NewDataTransfer(host host.Host, transport transport.Transport, storedCounter *storedcounter.StoredCounter) datatransfer.Manager {
	dataTransferNetwork := network.NewFromLibp2pHost(host)
	m := &manager{
		dataTransferNetwork: dataTransferNetwork,
		validatedTypes:      registry.NewRegistry(),
		pubSub:              pubsub.New(dispatcher),
		channels:            channels.New(),
		peerID:              host.ID(),
		transport:           transport,
		storedCounter:       storedCounter,
	}

	return m
}

// Start initializes data transfer processing
func (m *manager) Start(ctx context.Context) error {
	dtReceiver := &receiver{m}
	m.dataTransferNetwork.SetDelegate(dtReceiver)
	return m.transport.SetEventHandler(m)
}

// Stop terminates all data transfers and ends processing
func (m *manager) Stop() error {
	return nil
}

func (m *manager) OnChannelOpened(chid datatransfer.ChannelID) error {
	_, err := m.channels.GetByID(chid)
	return err
}

func (m *manager) OnDataReceived(chid datatransfer.ChannelID, link ipld.Link, size uint64) (message.DataTransferMessage, error) {
	_, err := m.channels.IncrementReceived(chid, size)
	if err != nil {
		return nil, err
	}
	chst, err := m.channels.GetByID(chid)
	if err != nil {
		return nil, err
	}
	evt := datatransfer.Event{
		Code:      datatransfer.Progress,
		Message:   fmt.Sprintf("Received %d more bytes", size),
		Timestamp: time.Now(),
	}
	err = m.pubSub.Publish(internalEvent{evt, chst})
	if err != nil {
		log.Warnf("err publishing DT event: %s", err.Error())
	}
	return nil, nil
}

func (m *manager) OnDataSent(chid datatransfer.ChannelID, link ipld.Link, size uint64) (message.DataTransferMessage, error) {
	_, err := m.channels.IncrementSent(chid, size)
	if err != nil {
		return nil, err
	}
	chst, err := m.channels.GetByID(chid)
	if err != nil {
		return nil, err
	}
	evt := datatransfer.Event{
		Code:      datatransfer.Progress,
		Message:   fmt.Sprintf("Sent %d more bytes", size),
		Timestamp: time.Now(),
	}
	err = m.pubSub.Publish(internalEvent{evt, chst})
	if err != nil {
		log.Warnf("err publishing DT event: %s", err.Error())
	}
	return nil, nil
}

func (m *manager) OnRequestReceived(chid datatransfer.ChannelID, request message.DataTransferRequest) (message.DataTransferResponse, error) {
	_, err := m.channels.GetByID(chid)
	if err != nil {
		return m.receiveRequest(chid.Initiator, request)
	}
	return nil, errors.New("Channel exists")
}

func (m *manager) OnResponseReceived(chid datatransfer.ChannelID, response message.DataTransferResponse) error {
	_, err := m.channels.GetByID(chid)
	return err
}

func (m *manager) OnResponseCompleted(chid datatransfer.ChannelID, success bool) error {
	chst, err := m.channels.GetByID(chid)
	if err != nil {
		return err
	}

	evt := datatransfer.Event{
		Code:      datatransfer.Error,
		Timestamp: time.Now(),
	}
	if success {
		evt.Code = datatransfer.Complete
	}
	err = m.pubSub.Publish(internalEvent{evt, chst})
	if err != nil {
		log.Warnf("err publishing DT event: %s", err.Error())
	}
	return nil
}

// RegisterVoucherType registers a validator for the given voucher type
// returns error if:
// * voucher type does not implement voucher
// * there is a voucher type registered with an identical identifier
// * voucherType's Kind is not reflect.Ptr
func (m *manager) RegisterVoucherType(voucherType datatransfer.Voucher, validator datatransfer.RequestValidator) error {
	err := m.validatedTypes.Register(voucherType, validator)
	if err != nil {
		return xerrors.Errorf("error registering voucher type: %w", err)
	}
	return nil
}

// OpenPushDataChannel opens a data transfer that will send data to the recipient peer and
// transfer parts of the piece that match the selector
func (m *manager) OpenPushDataChannel(ctx context.Context, requestTo peer.ID, voucher datatransfer.Voucher, baseCid cid.Cid, selector ipld.Node) (datatransfer.ChannelID, error) {
	req, err := m.newRequest(ctx, selector, false, voucher, baseCid, requestTo)
	if err != nil {
		return datatransfer.ChannelID{}, err
	}

	chid, err := m.channels.CreateNew(req.TransferID(), baseCid, selector, voucher,
		m.peerID, m.peerID, requestTo) // initiator = us, sender = us, receiver = them
	if err != nil {
		return chid, err
	}
	evt := datatransfer.Event{
		Code:      datatransfer.Open,
		Message:   "New Request Initiated",
		Timestamp: time.Now(),
	}
	chst, err := m.channels.GetByID(chid)
	if err != nil {
		return chid, err
	}
	if err := m.dataTransferNetwork.SendMessage(ctx, requestTo, req); err != nil {
		evt = datatransfer.Event{
			Code:      datatransfer.Error,
			Message:   "Unable to send request",
			Timestamp: time.Now(),
		}
	}
	err = m.pubSub.Publish(internalEvent{evt, chst})
	if err != nil {
		log.Warnf("err publishing DT event: %s", err.Error())
	}
	return chid, nil
}

// OpenPullDataChannel opens a data transfer that will request data from the sending peer and
// transfer parts of the piece that match the selector
func (m *manager) OpenPullDataChannel(ctx context.Context, requestTo peer.ID, voucher datatransfer.Voucher, baseCid cid.Cid, selector ipld.Node) (datatransfer.ChannelID, error) {
	req, err := m.newRequest(ctx, selector, true, voucher, baseCid, requestTo)
	if err != nil {
		return datatransfer.ChannelID{}, err
	}
	// initiator = us, sender = them, receiver = us
	chid, err := m.channels.CreateNew(req.TransferID(), baseCid, selector, voucher,
		m.peerID, requestTo, m.peerID)
	if err != nil {
		return chid, err
	}
	evt := datatransfer.Event{
		Code:      datatransfer.Open,
		Message:   "New Request Initiated",
		Timestamp: time.Now(),
	}
	chst, err := m.channels.GetByID(chid)
	if err != nil {
		return chid, err
	}
	if err := m.transport.OpenChannel(ctx, requestTo, chid, cidlink.Link{Cid: baseCid}, selector, req); err != nil {
		evt = datatransfer.Event{
			Code:      datatransfer.Error,
			Message:   "Unable to send request",
			Timestamp: time.Now(),
		}
	}
	err = m.pubSub.Publish(internalEvent{evt, chst})
	if err != nil {
		log.Warnf("err publishing DT event: %s", err.Error())
	}
	return chid, nil
}

// SendVoucher sends an intermediate voucher as needed when the receiver sends a request for revalidation
func (m *manager) SendVoucher(ctx context.Context, channelID datatransfer.ChannelID, voucher datatransfer.Voucher) error {
	chst, err := m.channels.GetByID(channelID)
	if err != nil {
		return err
	}
	if channelID.Initiator != m.peerID {
		return errors.New("cannot send voucher for request we did not initiate")
	}
	updateRequest, err := message.UpdateRequest(channelID.ID, false, voucher.Type(), voucher)
	if err != nil {
		return err
	}
	m.dataTransferNetwork.SendMessage(ctx, chst.OtherParty(m.peerID), updateRequest)
	return nil
}

// newRequest encapsulates message creation
func (m *manager) newRequest(ctx context.Context, selector ipld.Node, isPull bool, voucher datatransfer.Voucher, baseCid cid.Cid, to peer.ID) (message.DataTransferRequest, error) {
	next, err := m.storedCounter.Next()
	if err != nil {
		return nil, err
	}
	tid := datatransfer.TransferID(next)
	return message.NewRequest(tid, isPull, voucher.Type(), voucher, baseCid, selector)
}

func (m *manager) response(isAccepted bool, tid datatransfer.TransferID, voucherResult datatransfer.VoucherResult) (message.DataTransferResponse, error) {
	var resultType datatransfer.TypeIdentifier
	if voucherResult != nil {
		resultType = voucherResult.Type()
	}
	return message.NewResponse(tid, isAccepted, false, false, resultType, voucherResult)
}

// close an open channel (effectively a cancel)
func (m *manager) CloseDataTransferChannel(x datatransfer.ChannelID) {}

// get status of a transfer
func (m *manager) TransferChannelStatus(chid datatransfer.ChannelID) datatransfer.Status {
	return m.channels.GetStatus(chid)
}

// get notified when certain types of events happen
func (m *manager) SubscribeToEvents(subscriber datatransfer.Subscriber) datatransfer.Unsubscribe {
	return datatransfer.Unsubscribe(m.pubSub.Subscribe(subscriber))
}

// get all in progress transfers
func (m *manager) InProgressChannels() map[datatransfer.ChannelID]datatransfer.ChannelState {
	return m.channels.InProgress()
}

// RegisterRevalidator registers a revalidator for the given voucher type
// Note: this is the voucher type used to revalidate. It can share a name
// with the initial validator type and CAN be the same type, or a different type.
// The revalidator can simply be the sampe as the original request validator,
// or a different validator that satisfies the revalidator interface.
func (m *manager) RegisterRevalidator(voucherType datatransfer.Voucher, revalidator datatransfer.Revalidator) error {
	panic("not implemented")
}

// RegisterVoucherResultType allows deserialization of a voucher result,
// so that a listener can read the metadata
func (m *manager) RegisterVoucherResultType(resultType datatransfer.VoucherResult) error {
	panic("not implemented")
}

func (m *manager) receiveRequest(
	initiator peer.ID,
	incoming message.DataTransferRequest) (message.DataTransferResponse, error) {
	result, err := m.acceptRequest(initiator, incoming)
	if err != nil {
		log.Error(err)
	}
	return m.response(err == nil, incoming.TransferID(), result)
}

func (m *manager) acceptRequest(
	initiator peer.ID,
	incoming message.DataTransferRequest) (datatransfer.VoucherResult, error) {

	voucher, result, err := m.validateVoucher(initiator, incoming)
	if err != nil {
		return result, err
	}
	stor, _ := incoming.Selector()

	var dataSender, dataReceiver peer.ID
	if incoming.IsPull() {
		dataSender = m.peerID
		dataReceiver = initiator
	} else {
		dataSender = initiator
		dataReceiver = m.peerID
	}

	chid, err := m.channels.CreateNew(incoming.TransferID(), incoming.BaseCid(), stor, voucher, initiator, dataSender, dataReceiver)
	if err != nil {
		return result, err
	}
	evt := datatransfer.Event{
		Code:      datatransfer.Open,
		Message:   "Incoming request accepted",
		Timestamp: time.Now(),
	}
	chst, err := m.channels.GetByID(chid)
	if err != nil {
		return result, err
	}
	err = m.pubSub.Publish(internalEvent{evt, chst})
	if err != nil {
		log.Warnf("err publishing DT event: %s", err.Error())
	}
	return result, nil
}

// validateVoucher converts a voucher in an incoming message to its appropriate
// voucher struct, then runs the validator and returns the results.
// returns error if:
//   * reading voucher fails
//   * deserialization of selector fails
//   * validation fails
func (m *manager) validateVoucher(sender peer.ID, incoming message.DataTransferRequest) (datatransfer.Voucher, datatransfer.VoucherResult, error) {

	vtypStr := datatransfer.TypeIdentifier(incoming.VoucherType())
	decoder, has := m.validatedTypes.Decoder(vtypStr)
	if !has {
		return nil, nil, xerrors.Errorf("unknown voucher type: %s", vtypStr)
	}
	encodable, err := incoming.Voucher(decoder)
	if err != nil {
		return nil, nil, err
	}
	vouch := encodable.(datatransfer.Registerable)

	var validatorFunc func(peer.ID, datatransfer.Voucher, cid.Cid, ipld.Node) (datatransfer.VoucherResult, error)
	processor, _ := m.validatedTypes.Processor(vtypStr)
	validator := processor.(datatransfer.RequestValidator)
	if incoming.IsPull() {
		validatorFunc = validator.ValidatePull
	} else {
		validatorFunc = validator.ValidatePush
	}

	stor, err := incoming.Selector()
	if err != nil {
		return vouch, nil, err
	}

	result, err := validatorFunc(sender, vouch, incoming.BaseCid(), stor)
	if err != nil {
		return nil, result, err
	}

	return vouch, result, nil
}
