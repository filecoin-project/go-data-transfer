package message1_1

import (
	"io"

	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/datamodel"
	"github.com/ipld/go-ipld-prime/schema"
	xerrors "golang.org/x/xerrors"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	ipldutils "github.com/filecoin-project/go-data-transfer/v2/ipldutils"
	"github.com/filecoin-project/go-data-transfer/v2/message/types"
)

var emptyTypedVoucher = datatransfer.TypedVoucher{
	Voucher: ipld.Null,
	Type:    datatransfer.EmptyTypeIdentifier,
}

// NewRequest generates a new request for the data transfer protocol
func NewRequest(id datatransfer.TransferID, isRestart bool, isPull bool, voucher *datatransfer.TypedVoucher, baseCid cid.Cid, selector datamodel.Node) (datatransfer.Request, error) {
	if voucher == nil {
		voucher = &emptyTypedVoucher
	}
	if baseCid == cid.Undef {
		return nil, xerrors.Errorf("base CID must be defined")
	}

	var typ uint64
	if isRestart {
		typ = uint64(types.RestartMessage)
	} else {
		typ = uint64(types.NewMessage)
	}

	if voucher == nil {
		voucher = &emptyTypedVoucher
	}

	return &TransferRequest1_1{
		MessageType:           typ,
		Pull:                  isPull,
		VoucherPtr:            voucher.Voucher,
		SelectorPtr:           selector,
		BaseCidPtr:            &baseCid,
		VoucherTypeIdentifier: voucher.Type,
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
func VoucherRequest(id datatransfer.TransferID, voucher *datatransfer.TypedVoucher) (datatransfer.Request, error) {
	if voucher == nil {
		voucher = &emptyTypedVoucher
	}
	return &TransferRequest1_1{
		MessageType:           uint64(types.VoucherMessage),
		VoucherPtr:            voucher.Voucher,
		VoucherTypeIdentifier: voucher.Type,
		TransferId:            uint64(id),
	}, nil
}

// RestartResponse builds a new Data Transfer response
func RestartResponse(id datatransfer.TransferID, accepted bool, isPaused bool, voucherResult *datatransfer.TypedVoucher) (datatransfer.Response, error) {
	if voucherResult == nil {
		voucherResult = &emptyTypedVoucher
	}
	return &TransferResponse1_1{
		RequestAccepted:       accepted,
		MessageType:           uint64(types.RestartMessage),
		Paused:                isPaused,
		TransferId:            uint64(id),
		VoucherResultPtr:      voucherResult.Voucher,
		VoucherTypeIdentifier: voucherResult.Type,
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

	voucherResult := &emptyTypedVoucher
	if validationResult.VoucherResult != nil {
		voucherResult = validationResult.VoucherResult
	}
	return &TransferResponse1_1{
		// TODO: when we area able to change the protocol, it would be helpful to record
		// Validation errors vs rejections
		RequestAccepted:       validationErr == nil && validationResult.Accepted,
		MessageType:           uint64(messageType),
		Paused:                paused,
		TransferId:            uint64(id),
		VoucherTypeIdentifier: voucherResult.Type,
		VoucherResultPtr:      voucherResult.Voucher,
	}, nil
}

// NewResponse builds a new Data Transfer response
func NewResponse(id datatransfer.TransferID, accepted bool, isPaused bool, voucherResult *datatransfer.TypedVoucher) (datatransfer.Response, error) {
	if voucherResult == nil {
		voucherResult = &emptyTypedVoucher
	}
	return &TransferResponse1_1{
		RequestAccepted:       accepted,
		MessageType:           uint64(types.NewMessage),
		Paused:                isPaused,
		TransferId:            uint64(id),
		VoucherTypeIdentifier: voucherResult.Type,
		VoucherResultPtr:      voucherResult.Voucher,
	}, nil
}

// VoucherResultResponse builds a new response for a voucher result
func VoucherResultResponse(id datatransfer.TransferID, accepted bool, isPaused bool, voucherResult *datatransfer.TypedVoucher) (datatransfer.Response, error) {
	if voucherResult == nil {
		voucherResult = &emptyTypedVoucher
	}
	return &TransferResponse1_1{
		RequestAccepted:       accepted,
		MessageType:           uint64(types.VoucherResultMessage),
		Paused:                isPaused,
		TransferId:            uint64(id),
		VoucherTypeIdentifier: voucherResult.Type,
		VoucherResultPtr:      voucherResult.Voucher,
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
func CompleteResponse(id datatransfer.TransferID, isAccepted bool, isPaused bool, voucherResult *datatransfer.TypedVoucher) (datatransfer.Response, error) {
	if voucherResult == nil {
		voucherResult = &emptyTypedVoucher
	}
	return &TransferResponse1_1{
		MessageType:           uint64(types.CompleteMessage),
		RequestAccepted:       isAccepted,
		Paused:                isPaused,
		VoucherTypeIdentifier: voucherResult.Type,
		VoucherResultPtr:      voucherResult.Voucher,
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

	return fromMessage(tresp)
}

func fromMessage(tresp *TransferMessage1_1) (datatransfer.Message, error) {
	if (tresp.IsRequest && tresp.Request == nil) || (!tresp.IsRequest && tresp.Response == nil) {
		return nil, xerrors.Errorf("invalid/malformed message")
	}

	if tresp.IsRequest {
		return tresp.Request, nil
	}
	return tresp.Response, nil
}

// FromNetWrraped can read a network stream to deserialize a message + transport ID
func FromNetWrapped(r io.Reader) (datatransfer.TransportID, datatransfer.Message, error) {
	builder := Prototype.WrappedTransferMessage.Representation().NewBuilder()
	err := dagcbor.Decode(builder, r)
	if err != nil {
		return "", nil, err
	}
	node := builder.Build()
	wtresp := bindnode.Unwrap(node).(*WrappedTransferMessage1_1)
	msg, err := fromMessage(&wtresp.Message)
	return datatransfer.TransportID(wtresp.TransportID), msg, err
}

// FromNet can read a network stream to deserialize a GraphSyncMessage
func FromIPLD(node datamodel.Node) (datatransfer.Message, error) {
	if tn, ok := node.(schema.TypedNode); ok { // shouldn't need this if from Graphsync
		node = tn.Representation()
	}
	tm, err := ipldutils.FromNode(node, &TransferMessage1_1{})
	if err != nil {
		return nil, err
	}
	tresp := tm.(*TransferMessage1_1)
	return fromMessage(tresp)
}
