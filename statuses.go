package datatransfer

import "github.com/filecoin-project/go-statemachine/fsm"

// Status is the status of transfer for a given channel
type Status uint64

const (
	// Requested means a data transfer was requested by has not yet been approved
	Requested Status = iota

	// Ongoing means the data transfer is in progress
	Ongoing

	// TransferFinished indicates the initiator is done sending/receiving
	// data but is awaiting confirmation from the responder
	TransferFinished

	// ResponderCompleted indicates the initiator received a message from the
	// responder that it's completed
	ResponderCompleted

	// Finalizing means the responder is awaiting a final message from the initator to
	// consider the transfer done
	Finalizing

	// Completing just means we have some final cleanup for a completed request
	Completing

	// Completed means the data transfer is completed successfully
	Completed

	// Failing just means we have some final cleanup for a failed request
	Failing

	// Failed means the data transfer failed
	Failed

	// Cancelling just means we have some final cleanup for a cancelled request
	Cancelling

	// Cancelled means the data transfer ended prematurely
	Cancelled

	// DEPRECATED: Use InitiatorPaused() method on ChannelState
	InitiatorPaused

	// DEPRECATED: Use ResponderPaused() method on ChannelState
	ResponderPaused

	// DEPRECATED: Use BothPaused() method on ChannelState
	BothPaused

	// ResponderFinalizing is a unique state where the responder is awaiting a final voucher
	ResponderFinalizing

	// ResponderFinalizingTransferFinished is a unique state where the responder is awaiting a final voucher
	// and we have received all data
	ResponderFinalizingTransferFinished

	// ChannelNotFoundError means the searched for data transfer does not exist
	ChannelNotFoundError

	// Queued indicates a data transfer request has been accepted, but is not actively transfering yet
	Queued

	// AwaitingAcceptance indicates a transfer request is actively being processed by the transport
	// even if the remote has not yet responded that it's accepted the transfer. Such a state can
	// occur, for example, in a requestor-initiated transfer that starts processing prior to receiving
	// acceptance from the server.
	AwaitingAcceptance
)

type statusList []Status

func (sl statusList) Contains(s Status) bool {
	for _, ts := range sl {
		if ts == s {
			return true
		}
	}
	return false
}

func (sl statusList) AsFSMStates() []fsm.StateKey {
	sk := make([]fsm.StateKey, 0, len(sl))
	for _, s := range sl {
		sk = append(sk, s)
	}
	return sk
}

var NotAcceptedStates = statusList{
	Requested,
	AwaitingAcceptance,
	Cancelled,
	Cancelling,
	Failed,
	Failing,
	ChannelNotFoundError}

func (s Status) IsAccepted() bool {
	return !NotAcceptedStates.Contains(s)
}
func (s Status) String() string {
	return Statuses[s]
}

var FinalizationStatuses = statusList{Finalizing, Completed, Completing}

func (s Status) InFinalization() bool {
	return FinalizationStatuses.Contains(s)
}

var TransferCompleteStates = statusList{
	TransferFinished,
	ResponderFinalizingTransferFinished,
	Finalizing,
	Completed,
	Completing,
	Failing,
	Failed,
	Cancelling,
	Cancelled,
	ChannelNotFoundError,
}

func (s Status) TransferComplete() bool {
	return TransferCompleteStates.Contains(s)
}

var TransferringStates = statusList{
	Ongoing,
	ResponderCompleted,
	ResponderFinalizing,
	AwaitingAcceptance,
}

func (s Status) Transferring() bool {
	return TransferringStates.Contains(s)
}

// Statuses are human readable names for data transfer states
var Statuses = map[Status]string{
	// Requested means a data transfer was requested by has not yet been approved
	Requested:                           "Requested",
	Ongoing:                             "Ongoing",
	TransferFinished:                    "TransferFinished",
	ResponderCompleted:                  "ResponderCompleted",
	Finalizing:                          "Finalizing",
	Completing:                          "Completing",
	Completed:                           "Completed",
	Failing:                             "Failing",
	Failed:                              "Failed",
	Cancelling:                          "Cancelling",
	Cancelled:                           "Cancelled",
	InitiatorPaused:                     "InitiatorPaused",
	ResponderPaused:                     "ResponderPaused",
	BothPaused:                          "BothPaused",
	ResponderFinalizing:                 "ResponderFinalizing",
	ResponderFinalizingTransferFinished: "ResponderFinalizingTransferFinished",
	ChannelNotFoundError:                "ChannelNotFoundError",
	Queued:                              "Queued",
	AwaitingAcceptance:                  "AwaitingAcceptance",
}
