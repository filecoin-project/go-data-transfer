package message1_1

import (
	"io"

	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/schema"
	xerrors "golang.org/x/xerrors"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	ipldutils "github.com/filecoin-project/go-data-transfer/v2/ipldutils"
	"github.com/filecoin-project/go-data-transfer/v2/message/types"
)

// NewRequest generates a new request for the data transfer protocol
func NewRequest(id datatransfer.TransferID, isRestart bool, isPull bool, vtype datatransfer.TypeIdentifier, voucher ipld.Node, baseCid cid.Cid, selector ipld.Node) (datatransfer.Request, error) {
	if baseCid == cid.Undef {
		return nil, xerrors.Errorf("base CID must be defined")
	}

	var typ uint64
	if isRestart {
		typ = uint64(types.RestartMessage)
	} else {
		typ = uint64(types.NewMessage)
	}

	return &TransferRequest1_1{
		MessageType:           typ,
		Pull:                  isPull,
		VoucherPtr:            voucher,
		SelectorPtr:           selector,
		BaseCidPtr:            &baseCid,
		VoucherTypeIdentifier: vtype,
		TransferId:            uint64(id),
	}, nil
}

// RestartExistingChannelRequest creates a request to ask the other side to restart an existing channel
func RestartExistingChannelRequest(channelId datatransfer.ChannelID) datatransfer.Request {
	return &TransferRequest1_1{
		MessageType:    uint64(types.RestartExistingChannelRequestMessage),
		RestartChannel: channelId,
	}
}

// CancelRequest request generates a request to cancel an in progress request
func CancelRequest(id datatransfer.TransferID) datatransfer.Request {
	return &TransferRequest1_1{
		MessageType: uint64(types.CancelMessage),
		TransferId:  uint64(id),
	}
}

// UpdateRequest generates a new request update
func UpdateRequest(id datatransfer.TransferID, isPaused bool) datatransfer.Request {
	return &TransferRequest1_1{
		MessageType: uint64(types.UpdateMessage),
		Pause:       isPaused,
		TransferId:  uint64(id),
	}
}

// VoucherRequest generates a new request for the data transfer protocol
func VoucherRequest(id datatransfer.TransferID, vtype datatransfer.TypeIdentifier, voucher ipld.Node) (datatransfer.Request, error) {
	return &TransferRequest1_1{
		MessageType:           uint64(types.VoucherMessage),
		VoucherPtr:            voucher,
		VoucherTypeIdentifier: vtype,
		TransferId:            uint64(id),
	}, nil
}

// RestartResponse builds a new Data Transfer response
func RestartResponse(id datatransfer.TransferID, accepted bool, isPaused bool, voucherResultType datatransfer.TypeIdentifier, voucherResult ipld.Node) (datatransfer.Response, error) {
	return &TransferResponse1_1{
		RequestAccepted:       accepted,
		MessageType:           uint64(types.RestartMessage),
		Paused:                isPaused,
		TransferId:            uint64(id),
		VoucherTypeIdentifier: voucherResultType,
		VoucherResultPtr:      voucherResult,
	}, nil
}

// ValidationResultResponse response generates a response based on a validation result
// messageType determines what kind of response is created
func ValidationResultResponse(
	messageType types.MessageType,
	id datatransfer.TransferID,
	validationResult datatransfer.ValidationResult,
	validationErr error,
	paused bool) (datatransfer.Response, error) {

	voucherResultType := datatransfer.EmptyTypeIdentifier
	if validationResult.VoucherResult != nil && validationResult.VoucherResultType != "" {
		voucherResultType = validationResult.VoucherResultType
	}
	return &TransferResponse1_1{
		// TODO: when we area able to change the protocol, it would be helpful to record
		// Validation errors vs rejections
		RequestAccepted:       validationErr == nil && validationResult.Accepted,
		MessageType:           uint64(messageType),
		Paused:                paused,
		TransferId:            uint64(id),
		VoucherTypeIdentifier: voucherResultType,
		VoucherResultPtr:      validationResult.VoucherResult,
	}, nil
}

// NewResponse builds a new Data Transfer response
func NewResponse(id datatransfer.TransferID, accepted bool, isPaused bool, voucherResultType datatransfer.TypeIdentifier, voucherResult ipld.Node) (datatransfer.Response, error) {
	return &TransferResponse1_1{
		RequestAccepted:       accepted,
		MessageType:           uint64(types.NewMessage),
		Paused:                isPaused,
		TransferId:            uint64(id),
		VoucherTypeIdentifier: voucherResultType,
		VoucherResultPtr:      voucherResult,
	}, nil
}

// VoucherResultResponse builds a new response for a voucher result
func VoucherResultResponse(id datatransfer.TransferID, accepted bool, isPaused bool, voucherResultType datatransfer.TypeIdentifier, voucherResult ipld.Node) (datatransfer.Response, error) {
	return &TransferResponse1_1{
		RequestAccepted:       accepted,
		MessageType:           uint64(types.VoucherResultMessage),
		Paused:                isPaused,
		TransferId:            uint64(id),
		VoucherTypeIdentifier: voucherResultType,
		VoucherResultPtr:      voucherResult,
	}, nil
}

// UpdateResponse returns a new update response
func UpdateResponse(id datatransfer.TransferID, isPaused bool) datatransfer.Response {
	return &TransferResponse1_1{
		MessageType: uint64(types.UpdateMessage),
		Paused:      isPaused,
		TransferId:  uint64(id),
	}
}

// CancelResponse makes a new cancel response message
func CancelResponse(id datatransfer.TransferID) datatransfer.Response {
	return &TransferResponse1_1{
		MessageType: uint64(types.CancelMessage),
		TransferId:  uint64(id),
	}
}

// CompleteResponse returns a new complete response message
func CompleteResponse(id datatransfer.TransferID, isAccepted bool, isPaused bool, voucherResultType datatransfer.TypeIdentifier, voucherResult ipld.Node) (datatransfer.Response, error) {
	return &TransferResponse1_1{
		MessageType:           uint64(types.CompleteMessage),
		RequestAccepted:       isAccepted,
		Paused:                isPaused,
		VoucherTypeIdentifier: voucherResultType,
		VoucherResultPtr:      voucherResult,
		TransferId:            uint64(id),
	}, nil
}

// FromNet can read a network stream to deserialize a GraphSyncMessage
func FromNet(r io.Reader) (datatransfer.Message, error) {
	tm, err := ipldutils.FromReader(r, &TransferMessage1_1{})
	if err != nil {
		return nil, err
	}
	tresp := tm.(*TransferMessage1_1)

	if (tresp.IsRequest && tresp.Request == nil) || (!tresp.IsRequest && tresp.Response == nil) {
		return nil, xerrors.Errorf("invalid/malformed message")
	}

	if tresp.IsRequest {
		return tresp.Request, nil
	}
	return tresp.Response, nil
}

// FromNet can read a network stream to deserialize a GraphSyncMessage
func FromIPLD(node ipld.Node) (datatransfer.Message, error) {
	if tn, ok := node.(schema.TypedNode); ok { // shouldn't need this if from Graphsync
		node = tn.Representation()
	}
	tm, err := ipldutils.FromNode(node, &TransferMessage1_1{})
	if err != nil {
		return nil, err
	}
	tresp := tm.(*TransferMessage1_1)

	if (tresp.IsRequest && tresp.Request == nil) || (!tresp.IsRequest && tresp.Response == nil) {
		return nil, xerrors.Errorf("invalid/malformed message")
	}

	if tresp.IsRequest {
		return tresp.Request, nil
	}
	return tresp.Response, nil
}
