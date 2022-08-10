package channels

import (
	logging "github.com/ipfs/go-log/v2"
	"github.com/ipld/go-ipld-prime/datamodel"

	"github.com/filecoin-project/go-statemachine/fsm"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/channels/internal"
)

var log = logging.Logger("data-transfer")

// ChannelEvents describe the events taht can
var ChannelEvents = fsm.Events{
	// Open a channel
	fsm.Event(datatransfer.Open).FromAny().To(datatransfer.Requested).Action(func(chst *internal.ChannelState) error {
		chst.AddLog("")
		return nil
	}),

	// Remote peer has accepted the Open channel request
	fsm.Event(datatransfer.Accept).
		From(datatransfer.Requested).To(datatransfer.Queued).
		From(datatransfer.AwaitingAcceptance).To(datatransfer.Ongoing).
		Action(func(chst *internal.ChannelState) error {
			chst.AddLog("")
			return nil
		}),

	// The transport has indicated it's begun sending/receiving data
	fsm.Event(datatransfer.TransferInitiated).
		From(datatransfer.Requested).To(datatransfer.AwaitingAcceptance).
		From(datatransfer.Queued).To(datatransfer.Ongoing).
		From(datatransfer.Ongoing).ToJustRecord().
		Action(func(chst *internal.ChannelState) error {
			chst.AddLog("")
			return nil
		}),

	fsm.Event(datatransfer.Restart).FromAny().ToJustRecord().Action(func(chst *internal.ChannelState) error {
		chst.Message = ""
		chst.AddLog("")
		return nil
	}),

	fsm.Event(datatransfer.Cancel).FromAny().To(datatransfer.Cancelling).Action(func(chst *internal.ChannelState) error {
		chst.TransferClosed = true
		chst.AddLog("")
		return nil
	}),

	// When a channel is Opened, clear any previous error message.
	// (eg if the channel is opened after being restarted due to a connection
	// error)
	fsm.Event(datatransfer.Opened).FromAny().ToJustRecord().Action(func(chst *internal.ChannelState) error {
		chst.Message = ""
		chst.AddLog("")
		return nil
	}),

	fsm.Event(datatransfer.DataReceived).FromMany(datatransfer.TransferringStates.AsFSMStates()...).ToNoChange().
		Action(func(chst *internal.ChannelState, receivedIndex datamodel.Node) error {
			chst.ReceivedIndex = internal.CborGenCompatibleNode{Node: receivedIndex}
			chst.AddLog("")
			return nil
		}),
	fsm.Event(datatransfer.DataReceivedProgress).FromMany(datatransfer.TransferringStates.AsFSMStates()...).ToNoChange().
		Action(func(chst *internal.ChannelState, delta uint64) error {
			chst.Received += delta
			chst.AddLog("received data")
			return nil
		}),

	fsm.Event(datatransfer.DataSent).FromMany(datatransfer.TransferringStates.AsFSMStates()...).ToNoChange().
		Action(func(chst *internal.ChannelState, sentIndex datamodel.Node) error {
			chst.SentIndex = internal.CborGenCompatibleNode{Node: sentIndex}
			chst.AddLog("")
			return nil
		}),

	fsm.Event(datatransfer.DataSentProgress).FromMany(datatransfer.TransferringStates.AsFSMStates()...).ToNoChange().
		Action(func(chst *internal.ChannelState, delta uint64) error {
			chst.Sent += delta
			chst.AddLog("sending data")
			return nil
		}),

	fsm.Event(datatransfer.DataQueued).FromMany(datatransfer.TransferringStates.AsFSMStates()...).ToNoChange().
		Action(func(chst *internal.ChannelState, queuedIndex datamodel.Node) error {
			chst.QueuedIndex = internal.CborGenCompatibleNode{Node: queuedIndex}
			chst.AddLog("")
			return nil
		}),
	fsm.Event(datatransfer.DataQueuedProgress).FromMany(datatransfer.TransferringStates.AsFSMStates()...).ToNoChange().
		Action(func(chst *internal.ChannelState, delta uint64) error {
			chst.Queued += delta
			chst.AddLog("")
			return nil
		}),

	fsm.Event(datatransfer.SetDataLimit).FromAny().ToJustRecord().
		Action(func(chst *internal.ChannelState, dataLimit uint64) error {
			chst.DataLimit = dataLimit
			chst.AddLog("")
			return nil
		}),

	fsm.Event(datatransfer.SetRequiresFinalization).FromAny().ToJustRecord().
		Action(func(chst *internal.ChannelState, RequiresFinalization bool) error {
			chst.RequiresFinalization = RequiresFinalization
			chst.AddLog("")
			return nil
		}),

	fsm.Event(datatransfer.Disconnected).FromAny().ToNoChange().Action(func(chst *internal.ChannelState, err error) error {
		chst.Message = err.Error()
		chst.AddLog("data transfer disconnected: %s", chst.Message)
		return nil
	}),

	fsm.Event(datatransfer.SendDataError).FromAny().ToNoChange().Action(func(chst *internal.ChannelState, err error) error {
		chst.Message = err.Error()
		chst.AddLog("data transfer send error: %s", chst.Message)
		return nil
	}),

	fsm.Event(datatransfer.ReceiveDataError).FromAny().ToNoChange().Action(func(chst *internal.ChannelState, err error) error {
		chst.Message = err.Error()
		chst.AddLog("data transfer receive error: %s", chst.Message)
		return nil
	}),

	fsm.Event(datatransfer.SendMessageError).FromAny().ToNoChange().Action(func(chst *internal.ChannelState, err error) error {
		chst.Message = err.Error()
		chst.AddLog("data transfer errored sending message: %s", chst.Message)
		return nil
	}),

	fsm.Event(datatransfer.RequestCancelled).FromAny().ToNoChange().Action(func(chst *internal.ChannelState, err error) error {
		chst.Message = err.Error()
		chst.AddLog("data transfer request cancelled: %s", chst.Message)
		return nil
	}),

	fsm.Event(datatransfer.Error).FromAny().To(datatransfer.Failing).Action(func(chst *internal.ChannelState, err error) error {
		chst.Message = err.Error()
		chst.TransferClosed = true
		chst.AddLog("data transfer erred: %s", chst.Message)
		return nil
	}),

	fsm.Event(datatransfer.NewVoucher).FromAny().ToNoChange().
		Action(func(chst *internal.ChannelState, voucher datatransfer.TypedVoucher) error {
			chst.Vouchers = append(chst.Vouchers, internal.EncodedVoucher{Type: voucher.Type, Voucher: internal.CborGenCompatibleNode{Node: voucher.Voucher}})
			chst.AddLog("got new voucher")
			return nil
		}),

	fsm.Event(datatransfer.NewVoucherResult).FromAny().ToNoChange().
		Action(func(chst *internal.ChannelState, voucherResult datatransfer.TypedVoucher) error {
			chst.VoucherResults = append(chst.VoucherResults,
				internal.EncodedVoucherResult{Type: voucherResult.Type, VoucherResult: internal.CborGenCompatibleNode{Node: voucherResult.Voucher}})
			chst.AddLog("got new voucher result")
			return nil
		}),

	// TODO: There are four states from which the request can be "paused": request, queued, awaiting acceptance
	// and ongoing. There four states of being
	// paused (no pause, initiator pause, responder pause, both paused). Until the state machine software
	// supports orthogonal regions (https://en.wikipedia.org/wiki/UML_state_machine#Orthogonal_regions)
	// we end up with a cartesian product of states and as you can see, fairly complicated state transfers.
	// Previously, we had dealt with this by moving directly to the Ongoing state upon return from pause but this
	// seems less than ideal. We need some kind of support for pausing being an independent aspect of state
	// Possibly we should just remove whether a state is paused from the state entirely.
	fsm.Event(datatransfer.PauseInitiator).
		FromMany(datatransfer.Ongoing, datatransfer.Requested, datatransfer.Queued, datatransfer.AwaitingAcceptance).ToJustRecord().
		Action(func(chst *internal.ChannelState) error {
			chst.InitiatorPaused = true
			chst.AddLog("")
			return nil
		}),

	fsm.Event(datatransfer.PauseResponder).
		FromMany(datatransfer.Ongoing, datatransfer.Requested, datatransfer.Queued, datatransfer.AwaitingAcceptance, datatransfer.TransferFinished).ToJustRecord().
		Action(func(chst *internal.ChannelState) error {
			chst.ResponderPaused = true
			chst.AddLog("")
			return nil
		}),

	fsm.Event(datatransfer.DataLimitExceeded).
		FromMany(datatransfer.Ongoing, datatransfer.Requested, datatransfer.Queued, datatransfer.AwaitingAcceptance, datatransfer.ResponderCompleted, datatransfer.ResponderFinalizing).ToJustRecord().
		Action(func(chst *internal.ChannelState) error {
			chst.ResponderPaused = true
			chst.AddLog("")
			return nil
		}),

	fsm.Event(datatransfer.ResumeInitiator).
		FromMany(datatransfer.Ongoing, datatransfer.Requested, datatransfer.Queued, datatransfer.AwaitingAcceptance, datatransfer.ResponderCompleted, datatransfer.ResponderFinalizing).ToJustRecord().
		Action(func(chst *internal.ChannelState) error {
			chst.InitiatorPaused = false
			chst.AddLog("")
			return nil
		}),

	fsm.Event(datatransfer.ResumeResponder).
		FromMany(datatransfer.Ongoing, datatransfer.Requested, datatransfer.Queued, datatransfer.AwaitingAcceptance, datatransfer.TransferFinished).ToJustRecord().
		From(datatransfer.Finalizing).To(datatransfer.Completing).
		Action(func(chst *internal.ChannelState) error {
			chst.ResponderPaused = false
			chst.AddLog("")
			return nil
		}),

	// The transfer has finished on the local node - all data was sent / received
	fsm.Event(datatransfer.FinishTransfer).
		FromAny().To(datatransfer.TransferFinished).
		FromMany(datatransfer.Failing, datatransfer.Cancelling).ToJustRecord().
		From(datatransfer.ResponderCompleted).To(datatransfer.Completing).
		From(datatransfer.ResponderFinalizing).To(datatransfer.ResponderFinalizingTransferFinished).
		// If we are in the AwaitingAcceptance state, it means the other party simply never responded to our
		// our data transfer, or we never actually contacted them. In any case, it's safe to skip
		// the finalization process and complete the transfer
		From(datatransfer.AwaitingAcceptance).To(datatransfer.Completing).
		Action(func(chst *internal.ChannelState) error {
			chst.TransferClosed = true
			chst.AddLog("")
			return nil
		}),

	fsm.Event(datatransfer.ResponderBeginsFinalization).
		FromAny().To(datatransfer.ResponderFinalizing).
		FromMany(datatransfer.Failing, datatransfer.Cancelling).ToJustRecord().
		From(datatransfer.TransferFinished).To(datatransfer.ResponderFinalizingTransferFinished).
		FromMany(datatransfer.ResponderFinalizing, datatransfer.ResponderFinalizingTransferFinished).ToJustRecord().Action(func(chst *internal.ChannelState) error {
		chst.AddLog("")
		return nil
	}),

	// The remote peer sent a Complete message, meaning it has sent / received all data
	fsm.Event(datatransfer.ResponderCompletes).
		FromAny().To(datatransfer.ResponderCompleted).
		FromMany(datatransfer.Failing, datatransfer.Cancelling).ToJustRecord().
		From(datatransfer.TransferFinished).To(datatransfer.Completing).
		From(datatransfer.ResponderFinalizingTransferFinished).To(datatransfer.Completing).Action(func(chst *internal.ChannelState) error {
		chst.AddLog("")
		return nil
	}),

	fsm.Event(datatransfer.BeginFinalizing).FromAny().To(datatransfer.Finalizing).Action(func(chst *internal.ChannelState) error {
		chst.TransferClosed = true
		chst.AddLog("")
		return nil
	}),

	fsm.Event(datatransfer.CloseTransfer).FromAny().ToJustRecord().Action(func(chst *internal.ChannelState) error {
		chst.TransferClosed = true
		chst.AddLog("")
		return nil
	}),

	// Both the local node and the remote peer have completed the transfer
	fsm.Event(datatransfer.Complete).FromAny().To(datatransfer.Completing).Action(func(chst *internal.ChannelState) error {
		chst.TransferClosed = true
		chst.AddLog("")
		return nil
	}),

	fsm.Event(datatransfer.CleanupComplete).
		From(datatransfer.Cancelling).To(datatransfer.Cancelled).
		From(datatransfer.Failing).To(datatransfer.Failed).
		From(datatransfer.Completing).To(datatransfer.Completed).Action(func(chst *internal.ChannelState) error {
		chst.AddLog("")
		return nil
	}),

	// will kickoff state handlers for channels that were cleaning up
	fsm.Event(datatransfer.CompleteCleanupOnRestart).FromAny().ToNoChange().Action(func(chst *internal.ChannelState) error {
		chst.AddLog("")
		return nil
	}),
}

// ChannelStateEntryFuncs are handlers called as we enter different states
// (currently unused for this fsm)
var ChannelStateEntryFuncs = fsm.StateEntryFuncs{
	datatransfer.Cancelling: cleanupConnection,
	datatransfer.Failing:    cleanupConnection,
	datatransfer.Completing: cleanupConnection,
}

func cleanupConnection(ctx fsm.Context, env ChannelEnvironment, channel internal.ChannelState) error {
	otherParty := channel.Initiator
	if otherParty == env.ID() {
		otherParty = channel.Responder
	}
	env.CleanupChannel(datatransfer.ChannelID{ID: channel.TransferID, Initiator: channel.Initiator, Responder: channel.Responder})
	return ctx.Trigger(datatransfer.CleanupComplete)
}

// CleanupStates are the penultimate states for a channel
var CleanupStates = []fsm.StateKey{
	datatransfer.Cancelling,
	datatransfer.Completing,
	datatransfer.Failing,
}

// ChannelFinalityStates are the final states for a channel
var ChannelFinalityStates = []fsm.StateKey{
	datatransfer.Cancelled,
	datatransfer.Completed,
	datatransfer.Failed,
}

// IsChannelTerminated returns true if the channel is in a finality state
func IsChannelTerminated(st datatransfer.Status) bool {
	for _, s := range ChannelFinalityStates {
		if s == st {
			return true
		}
	}

	return false
}

// IsChannelCleaningUp returns true if channel was being cleaned up and finished
func IsChannelCleaningUp(st datatransfer.Status) bool {
	for _, s := range CleanupStates {
		if s == st {
			return true
		}
	}

	return false
}
