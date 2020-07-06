package channels

import (
	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-statemachine/fsm"
	logging "github.com/ipfs/go-log"
	cbg "github.com/whyrusleeping/cbor-gen"
)

var log = logging.Logger("data-transfer")

// ChannelEvents describe the events taht can
var ChannelEvents = fsm.Events{
	fsm.Event(datatransfer.Open).FromAny().To(datatransfer.Requested),
	fsm.Event(datatransfer.Accept).From(datatransfer.Requested).To(datatransfer.Ongoing),
	fsm.Event(datatransfer.Cancel).FromAny().To(datatransfer.Cancelled),
	fsm.Event(datatransfer.Progress).FromMany(
		datatransfer.Ongoing,
		datatransfer.SenderPaused,
		datatransfer.ReceiverPaused,
		datatransfer.ResponderCompleted).ToNoChange().Action(func(chst *internalChannelState, deltaSent uint64, deltaReceived uint64) error {
		chst.Received += deltaReceived
		chst.Sent += deltaSent
		return nil
	}),
	fsm.Event(datatransfer.Error).FromAny().To(datatransfer.Failed).Action(func(chst *internalChannelState, err error) error {
		chst.Message = err.Error()
		return nil
	}),
	fsm.Event(datatransfer.NewVoucher).FromAny().ToNoChange().
		Action(func(chst *internalChannelState, vtype datatransfer.TypeIdentifier, voucherBytes []byte) error {
			chst.Vouchers = append(chst.Vouchers, encodedVoucher{Type: vtype, Voucher: &cbg.Deferred{Raw: voucherBytes}})
			return nil
		}),
	fsm.Event(datatransfer.NewVoucherResult).FromAny().ToNoChange().
		Action(func(chst *internalChannelState, vtype datatransfer.TypeIdentifier, voucherResultBytes []byte) error {
			chst.VoucherResults = append(chst.VoucherResults,
				encodedVoucherResult{Type: vtype, VoucherResult: &cbg.Deferred{Raw: voucherResultBytes}})
			return nil
		}),
	fsm.Event(datatransfer.PauseSender).
		FromMany(datatransfer.Requested, datatransfer.Ongoing).To(datatransfer.SenderPaused).
		From(datatransfer.ReceiverPaused).To(datatransfer.BothPaused),
	fsm.Event(datatransfer.PauseReceiver).
		From(datatransfer.ResponderCompleted).To(datatransfer.ResponderCompletedReceiverPaused).
		FromMany(datatransfer.Requested, datatransfer.Ongoing).To(datatransfer.ReceiverPaused).
		From(datatransfer.SenderPaused).To(datatransfer.BothPaused),
	fsm.Event(datatransfer.ResumeSender).
		From(datatransfer.SenderPaused).To(datatransfer.Ongoing).
		From(datatransfer.BothPaused).To(datatransfer.ReceiverPaused),
	fsm.Event(datatransfer.ResumeReceiver).
		From(datatransfer.ReceiverPaused).To(datatransfer.Ongoing).
		From(datatransfer.BothPaused).To(datatransfer.SenderPaused).
		From(datatransfer.ResponderCompletedReceiverPaused).To(datatransfer.ResponderCompleted),
	fsm.Event(datatransfer.FinishTransfer).
		FromAny().To(datatransfer.TransferFinished).
		FromMany(datatransfer.ResponderCompleted, datatransfer.ResponderCompletedReceiverPaused).To(datatransfer.Completed),
	fsm.Event(datatransfer.CompleteResponder).
		FromAny().To(datatransfer.ResponderCompleted).
		From(datatransfer.ReceiverPaused).To(datatransfer.ResponderCompletedReceiverPaused).
		From(datatransfer.TransferFinished).To(datatransfer.Completed),
	fsm.Event(datatransfer.Complete).FromAny().To(datatransfer.Completed),
	fsm.Event(noopSynchronize).FromAny().ToNoChange(),
}

// ChannelStateEntryFuncs are handlers called as we enter different states
// (currently unused for this fsm)
var ChannelStateEntryFuncs = fsm.StateEntryFuncs{}

// ChannelFinalityStates are the final states for a channel
var ChannelFinalityStates = []fsm.StateKey{
	datatransfer.Cancelled,
	datatransfer.Completed,
	datatransfer.Failed,
}
