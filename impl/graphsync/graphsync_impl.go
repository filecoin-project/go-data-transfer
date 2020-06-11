package graphsyncimpl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-graphsync"
	logging "github.com/ipfs/go-log"
	"github.com/ipld/go-ipld-prime"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/peer"
	"golang.org/x/xerrors"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/channels"
	"github.com/filecoin-project/go-data-transfer/impl/graphsync/extension"
	"github.com/filecoin-project/go-data-transfer/impl/graphsync/hooks"
	"github.com/filecoin-project/go-data-transfer/message"
	"github.com/filecoin-project/go-data-transfer/network"
	"github.com/filecoin-project/go-data-transfer/registry"
	"github.com/filecoin-project/go-storedcounter"
	"github.com/hannahhoward/go-pubsub"
)

var log = logging.Logger("graphsync-impl")

type graphsyncImpl struct {
	dataTransferNetwork network.DataTransferNetwork
	validatedTypes      *registry.Registry
	pubSub              *pubsub.PubSub
	channels            *channels.Channels
	gs                  graphsync.GraphExchange
	peerID              peer.ID
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

// NewGraphSyncDataTransfer initializes a new graphsync based data transfer manager
func NewGraphSyncDataTransfer(host host.Host, gs graphsync.GraphExchange, storedCounter *storedcounter.StoredCounter) datatransfer.Manager {
	dataTransferNetwork := network.NewFromLibp2pHost(host)
	impl := &graphsyncImpl{
		dataTransferNetwork: dataTransferNetwork,
		validatedTypes:      registry.NewRegistry(),
		pubSub:              pubsub.New(dispatcher),
		channels:            channels.New(),
		gs:                  gs,
		peerID:              host.ID(),
		storedCounter:       storedCounter,
	}

	dtReceiver := &graphsyncReceiver{impl}
	dataTransferNetwork.SetDelegate(dtReceiver)

	hooksManager := hooks.NewManager(host.ID(), impl)
	hooksManager.RegisterHooks(gs)
	return impl
}

func (impl *graphsyncImpl) OnChannelOpened(chid datatransfer.ChannelID) error {
	_, err := impl.channels.GetByID(chid)
	return err
}

func (impl *graphsyncImpl) OnDataReceived(chid datatransfer.ChannelID, link ipld.Link, size uint64) (message.DataTransferMessage, error) {
	_, err := impl.channels.IncrementReceived(chid, size)
	if err != nil {
		return nil, err
	}
	chst, err := impl.channels.GetByID(chid)
	if err != nil {
		return nil, err
	}
	evt := datatransfer.Event{
		Code:      datatransfer.Progress,
		Message:   fmt.Sprintf("Received %d more bytes", size),
		Timestamp: time.Now(),
	}
	err = impl.pubSub.Publish(internalEvent{evt, chst})
	if err != nil {
		log.Warnf("err publishing DT event: %s", err.Error())
	}
	return nil, nil
}

func (impl *graphsyncImpl) OnDataSent(chid datatransfer.ChannelID, link ipld.Link, size uint64) (message.DataTransferMessage, error) {
	_, err := impl.channels.IncrementSent(chid, size)
	if err != nil {
		return nil, err
	}
	chst, err := impl.channels.GetByID(chid)
	if err != nil {
		return nil, err
	}
	evt := datatransfer.Event{
		Code:      datatransfer.Progress,
		Message:   fmt.Sprintf("Sent %d more bytes", size),
		Timestamp: time.Now(),
	}
	err = impl.pubSub.Publish(internalEvent{evt, chst})
	if err != nil {
		log.Warnf("err publishing DT event: %s", err.Error())
	}
	return nil, nil
}

func (impl *graphsyncImpl) OnRequestReceived(chid datatransfer.ChannelID, request message.DataTransferRequest) (message.DataTransferResponse, error) {
	_, err := impl.channels.GetByID(chid)
	if err != nil {
		return impl.receiveRequest(chid.Initiator, request)
	}
	return nil, err
}

func (impl *graphsyncImpl) OnResponseReceived(chid datatransfer.ChannelID, response message.DataTransferResponse) error {
	_, err := impl.channels.GetByID(chid)
	return err
}

func (impl *graphsyncImpl) OnResponseCompleted(chid datatransfer.ChannelID, success bool) error {
	chst, err := impl.channels.GetByID(chid)
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
	err = impl.pubSub.Publish(internalEvent{evt, chst})
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
func (impl *graphsyncImpl) RegisterVoucherType(voucherType datatransfer.Voucher, validator datatransfer.RequestValidator) error {
	err := impl.validatedTypes.Register(voucherType, validator)
	if err != nil {
		return xerrors.Errorf("error registering voucher type: %w", err)
	}
	return nil
}

// OpenPushDataChannel opens a data transfer that will send data to the recipient peer and
// transfer parts of the piece that match the selector
func (impl *graphsyncImpl) OpenPushDataChannel(ctx context.Context, requestTo peer.ID, voucher datatransfer.Voucher, baseCid cid.Cid, selector ipld.Node) (datatransfer.ChannelID, error) {
	req, err := impl.newRequest(ctx, selector, false, voucher, baseCid, requestTo)
	if err != nil {
		return datatransfer.ChannelID{}, err
	}

	chid, err := impl.channels.CreateNew(req.TransferID(), baseCid, selector, voucher,
		impl.peerID, impl.peerID, requestTo) // initiator = us, sender = us, receiver = them
	if err != nil {
		return chid, err
	}
	evt := datatransfer.Event{
		Code:      datatransfer.Open,
		Message:   "New Request Initiated",
		Timestamp: time.Now(),
	}
	chst, err := impl.channels.GetByID(chid)
	if err != nil {
		return chid, err
	}
	if err := impl.dataTransferNetwork.SendMessage(ctx, requestTo, req); err != nil {
		evt = datatransfer.Event{
			Code:      datatransfer.Error,
			Message:   "Unable to send request",
			Timestamp: time.Now(),
		}
	}
	err = impl.pubSub.Publish(internalEvent{evt, chst})
	if err != nil {
		log.Warnf("err publishing DT event: %s", err.Error())
	}
	return chid, nil
}

// OpenPullDataChannel opens a data transfer that will request data from the sending peer and
// transfer parts of the piece that match the selector
func (impl *graphsyncImpl) OpenPullDataChannel(ctx context.Context, requestTo peer.ID, voucher datatransfer.Voucher, baseCid cid.Cid, selector ipld.Node) (datatransfer.ChannelID, error) {
	req, err := impl.newRequest(ctx, selector, true, voucher, baseCid, requestTo)
	if err != nil {
		return datatransfer.ChannelID{}, err
	}
	// initiator = us, sender = them, receiver = us
	chid, err := impl.channels.CreateNew(req.TransferID(), baseCid, selector, voucher,
		impl.peerID, requestTo, impl.peerID)
	if err != nil {
		return chid, err
	}
	evt := datatransfer.Event{
		Code:      datatransfer.Open,
		Message:   "New Request Initiated",
		Timestamp: time.Now(),
	}
	chst, err := impl.channels.GetByID(chid)
	if err != nil {
		return chid, err
	}
	if err := impl.sendGsRequest(ctx, impl.peerID, requestTo, cidlink.Link{Cid: baseCid}, selector, req); err != nil {
		evt = datatransfer.Event{
			Code:      datatransfer.Error,
			Message:   "Unable to send request",
			Timestamp: time.Now(),
		}
	}
	err = impl.pubSub.Publish(internalEvent{evt, chst})
	if err != nil {
		log.Warnf("err publishing DT event: %s", err.Error())
	}
	return chid, nil
}

// SendVoucher sends an intermediate voucher as needed when the receiver sends a request for revalidation
func (impl *graphsyncImpl) SendVoucher(ctx context.Context, channelID datatransfer.ChannelID, voucher datatransfer.Voucher) error {
	chst, err := impl.channels.GetByID(channelID)
	if err != nil {
		return err
	}
	if channelID.Initiator != impl.peerID {
		return errors.New("cannot send voucher for request we did not initiate")
	}
	updateRequest, err := message.UpdateRequest(channelID.ID, false, voucher.Type(), voucher)
	if err != nil {
		return err
	}
	impl.dataTransferNetwork.SendMessage(ctx, chst.OtherParty(impl.peerID), updateRequest)
	return nil
}

// newRequest encapsulates message creation
func (impl *graphsyncImpl) newRequest(ctx context.Context, selector ipld.Node, isPull bool, voucher datatransfer.Voucher, baseCid cid.Cid, to peer.ID) (message.DataTransferRequest, error) {
	next, err := impl.storedCounter.Next()
	if err != nil {
		return nil, err
	}
	tid := datatransfer.TransferID(next)
	return message.NewRequest(tid, isPull, voucher.Type(), voucher, baseCid, selector)
}

func (impl *graphsyncImpl) response(isAccepted bool, tid datatransfer.TransferID, voucherResult datatransfer.VoucherResult) (message.DataTransferResponse, error) {
	var resultType datatransfer.TypeIdentifier
	if voucherResult != nil {
		resultType = voucherResult.Type()
	}
	return message.NewResponse(tid, isAccepted, false, false, resultType, voucherResult)
}

// close an open channel (effectively a cancel)
func (impl *graphsyncImpl) CloseDataTransferChannel(x datatransfer.ChannelID) {}

// get status of a transfer
func (impl *graphsyncImpl) TransferChannelStatus(x datatransfer.ChannelID) datatransfer.Status {
	return datatransfer.ChannelNotFoundError
}

// get notified when certain types of events happen
func (impl *graphsyncImpl) SubscribeToEvents(subscriber datatransfer.Subscriber) datatransfer.Unsubscribe {
	return datatransfer.Unsubscribe(impl.pubSub.Subscribe(subscriber))
}

// get all in progress transfers
func (impl *graphsyncImpl) InProgressChannels() map[datatransfer.ChannelID]datatransfer.ChannelState {
	return impl.channels.InProgress()
}

// sendGsRequest assembles a graphsync request and determines if the transfer was completed/successful.
// notifies subscribers of final request status.
func (impl *graphsyncImpl) sendGsRequest(ctx context.Context, initiator peer.ID, dataSender peer.ID, root cidlink.Link, stor ipld.Node, msg message.DataTransferMessage) error {
	buf := new(bytes.Buffer)
	err := msg.ToNet(buf)
	if err != nil {
		return err
	}
	extData := buf.Bytes()
	_, errChan := impl.gs.Request(ctx, dataSender, root, stor,
		graphsync.ExtensionData{
			Name: extension.ExtensionDataTransfer,
			Data: extData,
		})
	go func() {
		var lastError error
		for err := range errChan {
			lastError = err
		}
		evt := datatransfer.Event{
			Code:      datatransfer.Error,
			Timestamp: time.Now(),
		}
		chid := datatransfer.ChannelID{Initiator: initiator, ID: msg.TransferID()}
		chst, err := impl.channels.GetByID(chid)
		if err != nil {
			msg := "cannot find a matching channel for this request"
			evt.Message = msg
		} else {
			if lastError == nil {
				evt.Code = datatransfer.Complete
			} else {
				evt.Message = lastError.Error()
			}
		}
		err = impl.pubSub.Publish(internalEvent{evt, chst})
		if err != nil {
			log.Warnf("err publishing DT event: %s", err.Error())
		}
	}()
	return nil
}

// RegisterRevalidator registers a revalidator for the given voucher type
// Note: this is the voucher type used to revalidate. It can share a name
// with the initial validator type and CAN be the same type, or a different type.
// The revalidator can simply be the sampe as the original request validator,
// or a different validator that satisfies the revalidator interface.
func (impl *graphsyncImpl) RegisterRevalidator(voucherType datatransfer.Voucher, revalidator datatransfer.Revalidator) error {
	panic("not implemented")
}

// RegisterVoucherResultType allows deserialization of a voucher result,
// so that a listener can read the metadata
func (impl *graphsyncImpl) RegisterVoucherResultType(resultType datatransfer.VoucherResult) error {
	panic("not implemented")
}

func (impl *graphsyncImpl) receiveRequest(
	initiator peer.ID,
	incoming message.DataTransferRequest) (message.DataTransferResponse, error) {
	result, err := impl.acceptRequest(initiator, incoming)
	if err != nil {
		log.Error(err)
	}
	return impl.response(err == nil, incoming.TransferID(), result)
}

func (impl *graphsyncImpl) acceptRequest(
	initiator peer.ID,
	incoming message.DataTransferRequest) (datatransfer.VoucherResult, error) {

	voucher, result, err := impl.validateVoucher(initiator, incoming)
	if err != nil {
		return result, err
	}
	stor, _ := incoming.Selector()

	var dataSender, dataReceiver peer.ID
	if incoming.IsPull() {
		dataSender = impl.peerID
		dataReceiver = initiator
	} else {
		dataSender = initiator
		dataReceiver = impl.peerID
	}

	chid, err := impl.channels.CreateNew(incoming.TransferID(), incoming.BaseCid(), stor, voucher, initiator, dataSender, dataReceiver)
	if err != nil {
		return result, err
	}
	evt := datatransfer.Event{
		Code:      datatransfer.Open,
		Message:   "Incoming request accepted",
		Timestamp: time.Now(),
	}
	chst, err := impl.channels.GetByID(chid)
	if err != nil {
		return result, err
	}
	err = impl.pubSub.Publish(internalEvent{evt, chst})
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
func (impl *graphsyncImpl) validateVoucher(sender peer.ID, incoming message.DataTransferRequest) (datatransfer.Voucher, datatransfer.VoucherResult, error) {

	vtypStr := datatransfer.TypeIdentifier(incoming.VoucherType())
	decoder, has := impl.validatedTypes.Decoder(vtypStr)
	if !has {
		return nil, nil, xerrors.Errorf("unknown voucher type: %s", vtypStr)
	}
	encodable, err := incoming.Voucher(decoder)
	if err != nil {
		return nil, nil, err
	}
	vouch := encodable.(datatransfer.Registerable)

	var validatorFunc func(peer.ID, datatransfer.Voucher, cid.Cid, ipld.Node) (datatransfer.VoucherResult, error)
	processor, _ := impl.validatedTypes.Processor(vtypStr)
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
