package channels

import (
	"context"
	"errors"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	"github.com/ipld/go-ipld-prime/datamodel"
	peer "github.com/libp2p/go-libp2p-core/peer"
	"golang.org/x/xerrors"

	versioning "github.com/filecoin-project/go-ds-versioning/pkg"
	versionedfsm "github.com/filecoin-project/go-ds-versioning/pkg/fsm"
	"github.com/filecoin-project/go-statemachine"
	"github.com/filecoin-project/go-statemachine/fsm"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/channels/internal"
	"github.com/filecoin-project/go-data-transfer/v2/channels/internal/migrations"
)

type Notifier func(datatransfer.Event, datatransfer.ChannelState)

// ErrWrongType is returned when a caller attempts to change the type of implementation data after setting it
var ErrWrongType = errors.New("Cannot change type of implementation specific data after setting it")

// Channels is a thread safe list of channels
type Channels struct {
	notifier             Notifier
	stateMachines        fsm.Group
	migrateStateMachines func(context.Context) error
}

// ChannelEnvironment -- just a proxy for DTNetwork for now
type ChannelEnvironment interface {
	ID() peer.ID
	CleanupChannel(chid datatransfer.ChannelID)
}

// New returns a new thread safe list of channels
func New(ds datastore.Batching,
	notifier Notifier,
	env ChannelEnvironment,
	selfPeer peer.ID) (*Channels, error) {

	c := &Channels{notifier: notifier}
	channelMigrations, err := migrations.GetChannelStateMigrations(selfPeer)
	if err != nil {
		return nil, err
	}
	c.stateMachines, c.migrateStateMachines, err = versionedfsm.NewVersionedFSM(ds, fsm.Parameters{
		Environment:     env,
		StateType:       internal.ChannelState{},
		StateKeyField:   "Status",
		Events:          ChannelEvents,
		StateEntryFuncs: ChannelStateEntryFuncs,
		Notifier:        c.dispatch,
		FinalityStates:  ChannelFinalityStates,
	}, channelMigrations, versioning.VersionKey("2"))
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Start migrates the channel data store as needed
func (c *Channels) Start(ctx context.Context) error {
	return c.migrateStateMachines(ctx)
}

func (c *Channels) dispatch(eventName fsm.EventName, channel fsm.StateType) {
	evtCode, ok := eventName.(datatransfer.EventCode)
	if !ok {
		log.Errorf("dropped bad event %v", eventName)
	}
	realChannel, ok := channel.(internal.ChannelState)
	if !ok {
		log.Errorf("not a ClientDeal %v", channel)
	}
	evt := datatransfer.Event{
		Code:      evtCode,
		Message:   realChannel.Message,
		Timestamp: time.Now(),
	}
	log.Debugw("process data transfer listeners", "name", datatransfer.Events[evtCode], "transfer ID", realChannel.TransferID)
	c.notifier(evt, c.fromInternalChannelState(realChannel))
}

// CreateNew creates a new channel id and channel state and saves to channels.
// returns error if the channel exists already.
func (c *Channels) CreateNew(selfPeer peer.ID, tid datatransfer.TransferID, baseCid cid.Cid, selector datamodel.Node, voucher datatransfer.TypedVoucher, initiator, dataSender, dataReceiver peer.ID) (datatransfer.ChannelID, datatransfer.Channel, error) {
	var responder peer.ID
	if dataSender == initiator {
		responder = dataReceiver
	} else {
		responder = dataSender
	}
	chid := datatransfer.ChannelID{Initiator: initiator, Responder: responder, ID: tid}
	channel := &internal.ChannelState{
		SelfPeer:   selfPeer,
		TransferID: tid,
		Initiator:  initiator,
		Responder:  responder,
		BaseCid:    baseCid,
		Selector:   internal.CborGenCompatibleNode{Node: selector},
		Sender:     dataSender,
		Recipient:  dataReceiver,
		Stages:     &datatransfer.ChannelStages{},
		Vouchers: []internal.EncodedVoucher{
			{
				Type:    voucher.Type,
				Voucher: internal.CborGenCompatibleNode{voucher.Voucher},
			},
		},
		Status: datatransfer.Requested,
	}
	err := c.stateMachines.Begin(chid, channel)
	if err != nil {
		log.Errorw("failed to create new tracking channel for data-transfer", "channelID", chid, "err", err)
		return datatransfer.ChannelID{}, nil, err
	}
	log.Debugw("created tracking channel for data-transfer, emitting channel Open event", "channelID", chid)
	return chid, c.fromInternalChannelState(*channel), c.stateMachines.Send(chid, datatransfer.Open)
}

// InProgress returns a list of in progress channels
func (c *Channels) InProgress() (map[datatransfer.ChannelID]datatransfer.ChannelState, error) {
	var internalChannels []internal.ChannelState
	err := c.stateMachines.List(&internalChannels)
	if err != nil {
		return nil, err
	}
	channels := make(map[datatransfer.ChannelID]datatransfer.ChannelState, len(internalChannels))
	for _, internalChannel := range internalChannels {
		channels[datatransfer.ChannelID{ID: internalChannel.TransferID, Responder: internalChannel.Responder, Initiator: internalChannel.Initiator}] =
			c.fromInternalChannelState(internalChannel)
	}
	return channels, nil
}

// GetByID searches for a channel in the slice of channels with id `chid`.
// Returns datatransfer.EmptyChannelState if there is no channel with that id
func (c *Channels) GetByID(ctx context.Context, chid datatransfer.ChannelID) (datatransfer.ChannelState, error) {
	var internalChannel internal.ChannelState
	err := c.stateMachines.GetSync(ctx, chid, &internalChannel)
	if err != nil {
		return nil, datatransfer.ErrChannelNotFound
	}
	return c.fromInternalChannelState(internalChannel), nil
}

// Accept marks a data transfer as accepted
func (c *Channels) Accept(chid datatransfer.ChannelID) error {
	return c.send(chid, datatransfer.Accept)
}

func (c *Channels) ChannelOpened(chid datatransfer.ChannelID) error {
	return c.send(chid, datatransfer.Opened)
}

func (c *Channels) TransferInitiated(chid datatransfer.ChannelID) error {
	return c.send(chid, datatransfer.TransferInitiated)
}

// Restart marks a data transfer as restarted
func (c *Channels) Restart(chid datatransfer.ChannelID) error {
	return c.send(chid, datatransfer.Restart)
}

// CompleteCleanupOnRestart tells a channel to restart
func (c *Channels) CompleteCleanupOnRestart(chid datatransfer.ChannelID) error {
	return c.send(chid, datatransfer.CompleteCleanupOnRestart)
}

// DataSent records data being sent
func (c *Channels) DataSent(chid datatransfer.ChannelID, delta uint64, index datamodel.Node) error {
	return c.fireProgressEvent(chid, datatransfer.DataSent, datatransfer.DataSentProgress, delta, index)
}

// DataQueued records data being queued
func (c *Channels) DataQueued(chid datatransfer.ChannelID, delta uint64, index datamodel.Node) error {
	return c.fireProgressEvent(chid, datatransfer.DataQueued, datatransfer.DataQueuedProgress, delta, index)
}

// DataReceived records data being received
func (c *Channels) DataReceived(chid datatransfer.ChannelID, delta uint64, index datamodel.Node) error {
	return c.fireProgressEvent(chid, datatransfer.DataReceived, datatransfer.DataReceivedProgress, delta, index)
}

// DataLimitExceeded records a data limit exceeded event
func (c *Channels) DataLimitExceeded(chid datatransfer.ChannelID) error {
	return c.send(chid, datatransfer.DataLimitExceeded)
}

// PauseInitiator pauses the initator of this channel
func (c *Channels) PauseInitiator(chid datatransfer.ChannelID) error {
	return c.send(chid, datatransfer.PauseInitiator)
}

// PauseResponder pauses the responder of this channel
func (c *Channels) PauseResponder(chid datatransfer.ChannelID) error {
	return c.send(chid, datatransfer.PauseResponder)
}

// ResumeInitiator resumes the initator of this channel
func (c *Channels) ResumeInitiator(chid datatransfer.ChannelID) error {
	return c.send(chid, datatransfer.ResumeInitiator)
}

// ResumeResponder resumes the responder of this channel
func (c *Channels) ResumeResponder(chid datatransfer.ChannelID) error {
	return c.send(chid, datatransfer.ResumeResponder)
}

// NewVoucher records a new voucher for this channel
func (c *Channels) NewVoucher(chid datatransfer.ChannelID, voucher datatransfer.TypedVoucher) error {
	return c.send(chid, datatransfer.NewVoucher, voucher)
}

// NewVoucherResult records a new voucher result for this channel
func (c *Channels) NewVoucherResult(chid datatransfer.ChannelID, voucherResult datatransfer.TypedVoucher) error {
	return c.send(chid, datatransfer.NewVoucherResult, voucherResult)
}

// Complete indicates responder has completed sending/receiving data
func (c *Channels) Complete(chid datatransfer.ChannelID) error {
	return c.send(chid, datatransfer.Complete)
}

// FinishTransfer an initiator has finished sending/receiving data
func (c *Channels) FinishTransfer(chid datatransfer.ChannelID) error {
	return c.send(chid, datatransfer.FinishTransfer)
}

// ResponderCompletes indicates an initator has finished receiving data from a responder
func (c *Channels) ResponderCompletes(chid datatransfer.ChannelID) error {
	return c.send(chid, datatransfer.ResponderCompletes)
}

// ResponderBeginsFinalization indicates a responder has finished processing but is awaiting confirmation from the initiator
func (c *Channels) ResponderBeginsFinalization(chid datatransfer.ChannelID) error {
	return c.send(chid, datatransfer.ResponderBeginsFinalization)
}

// BeginFinalizing indicates a responder has finished processing but is awaiting confirmation from the initiator
func (c *Channels) BeginFinalizing(chid datatransfer.ChannelID) error {
	return c.send(chid, datatransfer.BeginFinalizing)
}

// Cancel indicates a channel was cancelled prematurely
func (c *Channels) Cancel(chid datatransfer.ChannelID) error {
	err := c.send(chid, datatransfer.Cancel)

	// If there was an error because the state machine already terminated,
	// sending a Cancel event is redundant anyway, so just ignore it
	if errors.Is(err, statemachine.ErrTerminated) {
		return nil
	}

	return err
}

// Error indicates something that went wrong on a channel
func (c *Channels) Error(chid datatransfer.ChannelID, err error) error {
	return c.send(chid, datatransfer.Error, err)
}

// Disconnected indicates that the connection went down and it was not possible
// to restart it
func (c *Channels) Disconnected(chid datatransfer.ChannelID, err error) error {
	return c.send(chid, datatransfer.Disconnected, err)
}

// RequestCancelled indicates that a transport layer request was cancelled by the
// request opener
func (c *Channels) RequestCancelled(chid datatransfer.ChannelID, err error) error {
	return c.send(chid, datatransfer.RequestCancelled, err)
}

// SendDataError indicates that the transport layer had an error trying
// to send data to the remote peer
func (c *Channels) SendDataError(chid datatransfer.ChannelID, err error) error {
	return c.send(chid, datatransfer.SendDataError, err)
}

// ReceiveDataError indicates that the transport layer had an error receiving
// data from the remote peer
func (c *Channels) ReceiveDataError(chid datatransfer.ChannelID, err error) error {
	return c.send(chid, datatransfer.ReceiveDataError, err)
}

// SendMessageError indicates an error sending a message to the transport layer
func (c *Channels) SendMessageError(chid datatransfer.ChannelID, err error) error {
	return c.send(chid, datatransfer.SendMessageError, err)
}

// SetDataLimit means a data limit has been set on this channel
func (c *Channels) SetDataLimit(chid datatransfer.ChannelID, dataLimit uint64) error {
	return c.send(chid, datatransfer.SetDataLimit, dataLimit)
}

// SetRequiresFinalization sets the state of whether a data transfer can complete
func (c *Channels) SetRequiresFinalization(chid datatransfer.ChannelID, RequiresFinalization bool) error {
	return c.send(chid, datatransfer.SetRequiresFinalization, RequiresFinalization)
}

// HasChannel returns true if the given channel id is being tracked
func (c *Channels) HasChannel(chid datatransfer.ChannelID) (bool, error) {
	return c.stateMachines.Has(chid)
}

// fireProgressEvent fires
// - an event for queuing / sending / receiving blocks
// - a corresponding "progress" event
func (c *Channels) fireProgressEvent(chid datatransfer.ChannelID, evt datatransfer.EventCode, progressEvt datatransfer.EventCode, delta uint64, index datamodel.Node) error {
	if err := c.checkChannelExists(chid, evt); err != nil {
		return err
	}

	// Fire the progress event
	if err := c.stateMachines.Send(chid, progressEvt, delta); err != nil {
		return err
	}

	// Fire the regular event
	return c.stateMachines.Send(chid, evt, index)
}

func (c *Channels) send(chid datatransfer.ChannelID, code datatransfer.EventCode, args ...interface{}) error {
	err := c.checkChannelExists(chid, code)
	if err != nil {
		return err
	}
	log.Debugw("send data transfer event", "name", datatransfer.Events[code], "transfer ID", chid.ID, "args", args)
	return c.stateMachines.Send(chid, code, args...)
}

func (c *Channels) checkChannelExists(chid datatransfer.ChannelID, code datatransfer.EventCode) error {
	has, err := c.stateMachines.Has(chid)
	if err != nil {
		return err
	}
	if !has {
		return xerrors.Errorf("cannot send FSM event %s to data-transfer channel %s: %w",
			datatransfer.Events[code], chid, datatransfer.ErrChannelNotFound)
	}
	return nil
}

// Convert from the internally used channel state format to the externally exposed ChannelState
func (c *Channels) fromInternalChannelState(ch internal.ChannelState) datatransfer.ChannelState {
	return fromInternalChannelState(ch)
}
