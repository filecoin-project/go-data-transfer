package impl

import (
	"context"
	"errors"
	"fmt"

	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	logging "github.com/ipfs/go-log"
	"github.com/ipld/go-ipld-prime"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
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
	resultTypes         *registry.Registry
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
func NewDataTransfer(ds datastore.Datastore, dataTransferNetwork network.DataTransferNetwork, transport transport.Transport, storedCounter *storedcounter.StoredCounter) (datatransfer.Manager, error) {
	m := &manager{
		dataTransferNetwork: dataTransferNetwork,
		validatedTypes:      registry.NewRegistry(),
		resultTypes:         registry.NewRegistry(),
		pubSub:              pubsub.New(dispatcher),
		peerID:              dataTransferNetwork.ID(),
		transport:           transport,
		storedCounter:       storedCounter,
	}
	channels, err := channels.New(ds, m.notifier, m.validatedTypes.Decoder, m.resultTypes.Decoder)
	if err != nil {
		return nil, err
	}
	m.channels = channels
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
	dtReceiver := &receiver{m}
	m.dataTransferNetwork.SetDelegate(dtReceiver)
	return m.transport.SetEventHandler(m)
}

// Stop terminates all data transfers and ends processing
func (m *manager) Stop() error {
	return nil
}

func (m *manager) OnChannelOpened(chid datatransfer.ChannelID) error {
	has, err := m.channels.HasChannel(chid)
	if err != nil {
		return err
	}
	if !has {
		return datatransfer.ErrChannelNotFound
	}
	return nil
}

func (m *manager) OnDataReceived(chid datatransfer.ChannelID, link ipld.Link, size uint64) error {
	return m.channels.IncrementReceived(chid, size)
}

func (m *manager) OnDataSent(chid datatransfer.ChannelID, link ipld.Link, size uint64) (message.DataTransferMessage, error) {
	err := m.channels.IncrementSent(chid, size)
	if err != nil {
		return nil, err
	}
	return nil, nil
}

func (m *manager) OnRequestReceived(chid datatransfer.ChannelID, request message.DataTransferRequest) (message.DataTransferResponse, error) {
	if request.IsNew() {
		return m.receiveNewRequest(chid.Initiator, request)
	}
	if request.IsCancel() {
		m.transport.CleanupChannel(chid)
		return nil, m.channels.Cancel(chid)
	}
	return m.receiveUpdateRequest(chid, request)
}

func (m *manager) OnResponseReceived(chid datatransfer.ChannelID, response message.DataTransferResponse) error {

	if response.IsCancel() {
		return m.channels.Cancel(chid)
	}
	if !response.EmptyVoucherResult() {
		vresult, err := m.decodeVoucherResult(response)
		if err != nil {
			return err
		}
		err = m.channels.NewVoucherResult(chid, vresult)
		if err != nil {
			return err
		}
	}
	if response.IsNew() {
		if response.Accepted() {
			return m.channels.Accept(chid)
		}
		return m.channels.Error(chid, errors.New("Response Rejected"))
	}
	chst, err := m.channels.GetByID(context.TODO(), chid)
	if err != nil {
		return err
	}
	if response.IsComplete() {
		if !m.isSender(chst, false) {
			return m.channels.CompleteResponder(chid)
		}
		return m.channels.Complete(chid)
	}
	err = m.processUpdatePauseStatus(chid, chst, response.IsPaused())
	if err != nil {
		return err
	}
	return nil
}

func (m *manager) OnChannelCompleted(chid datatransfer.ChannelID, success bool) error {
	if success {
		if chid.Initiator != m.peerID {
			if err := m.dataTransferNetwork.SendMessage(context.TODO(), chid.Initiator, message.CompleteResponse(chid.ID)); err != nil {
				_ = m.channels.Error(chid, err)
				return err
			}
			return m.channels.Complete(chid)
		}
		return m.channels.FinishTransfer(chid)
	}
	return m.channels.Error(chid, errors.New("incomplete response"))
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
	if err := m.dataTransferNetwork.SendMessage(ctx, requestTo, req); err != nil {
		err = fmt.Errorf("Unable to send request: %w", err)
		_ = m.channels.Error(chid, err)
		return chid, err
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
	if err := m.transport.OpenChannel(ctx, requestTo, chid, cidlink.Link{Cid: baseCid}, selector, req); err != nil {
		err = fmt.Errorf("Unable to send request: %w", err)
		_ = m.channels.Error(chid, err)
		return chid, err
	}
	return chid, nil
}

// SendVoucher sends an intermediate voucher as needed when the receiver sends a request for revalidation
func (m *manager) SendVoucher(ctx context.Context, channelID datatransfer.ChannelID, voucher datatransfer.Voucher) error {
	chst, err := m.channels.GetByID(ctx, channelID)
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
	if err := m.dataTransferNetwork.SendMessage(ctx, chst.OtherParty(m.peerID), updateRequest); err != nil {
		err = fmt.Errorf("Unable to send request: %w", err)
		_ = m.channels.Error(channelID, err)
		return err
	}
	return m.channels.NewVoucher(channelID, voucher)
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

func (m *manager) response(isAccepted bool, isPaused bool, tid datatransfer.TransferID, voucherResult datatransfer.VoucherResult) (message.DataTransferResponse, error) {
	resultType := datatransfer.EmptyTypeIdentifier
	if voucherResult != nil {
		resultType = voucherResult.Type()
	}
	return message.NewResponse(tid, isAccepted, isPaused, resultType, voucherResult)
}

// close an open channel (effectively a cancel)
func (m *manager) CloseDataTransferChannel(ctx context.Context, chid datatransfer.ChannelID) error {
	chst, err := m.channels.GetByID(ctx, chid)
	if err != nil {
		return err
	}
	err = m.transport.CloseChannel(ctx, chid)
	if err != nil {
		return err
	}

	if err := m.dataTransferNetwork.SendMessage(ctx, chst.OtherParty(m.peerID), m.cancelMessage(chid)); err != nil {
		err = fmt.Errorf("Unable to send cancel message: %w", err)
		_ = m.channels.Error(chid, err)
		return err
	}

	return m.channels.Cancel(chid)
}

// pause a running data transfer channel
func (m *manager) PauseDataTransferChannel(ctx context.Context, chid datatransfer.ChannelID) error {
	chst, err := m.channels.GetByID(ctx, chid)
	if err != nil {
		return err
	}

	if !m.isUnpaused(chst, false) {
		return errors.New("Cannot pause a request that is already paused")
	}

	pausable, ok := m.transport.(transport.PauseableTransport)
	if !ok {
		return errors.New("unsupported")
	}

	err = pausable.PauseChannel(ctx, chid)
	if err != nil {
		return err
	}

	pauseMessage, err := m.pauseMessage(chid)
	if err != nil {
		return err
	}

	if err := m.dataTransferNetwork.SendMessage(ctx, chst.OtherParty(m.peerID), pauseMessage); err != nil {
		err = fmt.Errorf("Unable to send pause message: %w", err)
		_ = m.channels.Error(chid, err)
		return err
	}

	return m.pause(chid, chst, false)
}

// resume a running data transfer channel
func (m *manager) ResumeDataTransferChannel(ctx context.Context, chid datatransfer.ChannelID) error {
	chst, err := m.channels.GetByID(ctx, chid)
	if err != nil {
		return err
	}

	if !m.isPaused(chst, false) {
		return errors.New("Cannot resume a request that is not paused")
	}

	pausable, ok := m.transport.(transport.PauseableTransport)
	if !ok {
		return errors.New("unsupported")
	}

	resumeMessage, err := m.resumeMessage(chid)
	if err != nil {
		return err
	}

	err = pausable.ResumeChannel(ctx, resumeMessage, chid)
	if err != nil {
		return err
	}

	return m.resume(chid, chst, false)
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
	return m.channels.InProgress(ctx)
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
	err := m.resultTypes.Register(resultType, nil)
	if err != nil {
		return xerrors.Errorf("error registering voucher type: %w", err)
	}
	return nil
}

func (m *manager) receiveNewRequest(
	initiator peer.ID,
	incoming message.DataTransferRequest) (message.DataTransferResponse, error) {
	result, err := m.acceptRequest(initiator, incoming)
	isAccepted := err == nil || err == datatransfer.ErrPause
	msg, msgErr := m.response(isAccepted, err == datatransfer.ErrPause, incoming.TransferID(), result)
	if msgErr != nil {
		return nil, msgErr
	}
	// convert to the transport error for pauses
	if err == datatransfer.ErrPause {
		err = transport.ErrPause
	}
	return msg, err
}

func (m *manager) acceptRequest(
	initiator peer.ID,
	incoming message.DataTransferRequest) (datatransfer.VoucherResult, error) {

	voucher, result, err := m.validateVoucher(initiator, incoming)
	if err != nil && err != datatransfer.ErrPause {
		return result, err
	}
	voucherErr := err
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
	if result != nil {
		err := m.channels.NewVoucherResult(chid, result)
		if err != nil {
			return result, err
		}
	}
	if err := m.channels.Accept(chid); err != nil {
		return result, err
	}
	return result, voucherErr
}

// validateVoucher converts a voucher in an incoming message to its appropriate
// voucher struct, then runs the validator and returns the results.
// returns error if:
//   * reading voucher fails
//   * deserialization of selector fails
//   * validation fails
func (m *manager) validateVoucher(sender peer.ID, incoming message.DataTransferRequest) (datatransfer.Voucher, datatransfer.VoucherResult, error) {
	vouch, err := m.decodeVoucher(incoming)
	if err != nil {
		return nil, nil, err
	}
	var validatorFunc func(peer.ID, datatransfer.Voucher, cid.Cid, ipld.Node) (datatransfer.VoucherResult, error)
	processor, _ := m.validatedTypes.Processor(vouch.Type())
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
	return vouch, result, err
}

type statusList []datatransfer.Status

func (sl statusList) Contains(s datatransfer.Status) bool {
	for _, ts := range sl {
		if ts == s {
			return true
		}
	}
	return false
}

func (m *manager) resume(chid datatransfer.ChannelID, chst datatransfer.ChannelState, useOtherParty bool) error {
	if m.isSender(chst, useOtherParty) {
		return m.channels.ResumeSender(chid)
	}
	return m.channels.ResumeReceiver(chid)
}

func (m *manager) isPaused(chst datatransfer.ChannelState, useOtherParty bool) bool {
	acceptable := statusList{datatransfer.BothPaused}
	if m.isSender(chst, useOtherParty) {
		acceptable = append(acceptable, datatransfer.SenderPaused)
	} else {
		acceptable = append(acceptable, datatransfer.ReceiverPaused, datatransfer.ResponderCompletedReceiverPaused)
	}
	return acceptable.Contains(chst.Status())
}

func (m *manager) pause(chid datatransfer.ChannelID, chst datatransfer.ChannelState, useOtherParty bool) error {
	if m.isSender(chst, useOtherParty) {
		return m.channels.PauseSender(chid)
	}
	return m.channels.PauseReceiver(chid)
}

func (m *manager) isUnpaused(chst datatransfer.ChannelState, useOtherParty bool) bool {
	acceptable := statusList{datatransfer.Ongoing}
	if m.isSender(chst, useOtherParty) {
		acceptable = append(acceptable, datatransfer.ReceiverPaused)
	} else {
		acceptable = append(acceptable, datatransfer.SenderPaused)
	}
	return acceptable.Contains(chst.Status())
}

func (m *manager) isSender(chst datatransfer.ChannelState, useOtherParty bool) bool {
	return (chst.Sender() == m.peerID) != useOtherParty
}

func (m *manager) resumeMessage(chid datatransfer.ChannelID) (message.DataTransferMessage, error) {
	if chid.Initiator == m.peerID {
		return message.UpdateRequest(chid.ID, false, datatransfer.EmptyTypeIdentifier, nil)
	}
	return message.UpdateResponse(chid.ID, true, false, datatransfer.EmptyTypeIdentifier, nil)
}

func (m *manager) pauseMessage(chid datatransfer.ChannelID) (message.DataTransferMessage, error) {
	if chid.Initiator == m.peerID {
		return message.UpdateRequest(chid.ID, true, datatransfer.EmptyTypeIdentifier, nil)
	}
	return message.UpdateResponse(chid.ID, true, true, datatransfer.EmptyTypeIdentifier, nil)
}

func (m *manager) cancelMessage(chid datatransfer.ChannelID) message.DataTransferMessage {
	if chid.Initiator == m.peerID {
		return message.CancelRequest(chid.ID)
	}
	return message.CancelResponse(chid.ID)
}

func (m *manager) decodeVoucherResult(response message.DataTransferResponse) (datatransfer.VoucherResult, error) {
	vtypStr := datatransfer.TypeIdentifier(response.VoucherResultType())
	decoder, has := m.resultTypes.Decoder(vtypStr)
	if !has {
		return nil, xerrors.Errorf("unknown voucher result type: %s", vtypStr)
	}
	encodable, err := response.VoucherResult(decoder)
	if err != nil {
		return nil, err
	}
	return encodable.(datatransfer.Registerable), nil
}

func (m *manager) decodeVoucher(request message.DataTransferRequest) (datatransfer.Voucher, error) {
	vtypStr := datatransfer.TypeIdentifier(request.VoucherType())
	decoder, has := m.validatedTypes.Decoder(vtypStr)
	if !has {
		return nil, xerrors.Errorf("unknown voucher type: %s", vtypStr)
	}
	encodable, err := request.Voucher(decoder)
	if err != nil {
		return nil, err
	}
	return encodable.(datatransfer.Registerable), nil
}

func (m *manager) receiveUpdateRequest(chid datatransfer.ChannelID, request message.DataTransferRequest) (message.DataTransferResponse, error) {
	chst, err := m.channels.GetByID(context.TODO(), chid)
	if err != nil {
		return nil, err
	}
	err = m.processUpdatePauseStatus(chid, chst, request.IsPaused())
	if err != nil {
		return nil, err
	}
	if !request.EmptyVoucher() {
		vouch, err := m.decodeVoucher(request)
		if err != nil {
			return nil, err
		}
		err = m.channels.NewVoucher(chid, vouch)
		if err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func (m *manager) processUpdatePauseStatus(chid datatransfer.ChannelID, chst datatransfer.ChannelState, isPaused bool) error {
	if isPaused {
		if m.isUnpaused(chst, true) {
			err := m.pause(chid, chst, true)
			if err != nil {
				return err
			}
		}
	} else {
		if m.isPaused(chst, true) {
			err := m.resume(chid, chst, true)
			if err != nil {
				return err
			}
			// if we were previously paused on both sides, still stay paused
			if chst.Status() == datatransfer.BothPaused {
				return transport.ErrPause
			}
		}
	}
	return nil
}
