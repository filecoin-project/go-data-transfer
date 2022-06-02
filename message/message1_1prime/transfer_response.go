package message1_1

import (
	"io"

	"github.com/ipld/go-ipld-prime/datamodel"
	"github.com/ipld/go-ipld-prime/schema"
	xerrors "golang.org/x/xerrors"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	ipldutils "github.com/filecoin-project/go-data-transfer/v2/ipldutils"
	"github.com/filecoin-project/go-data-transfer/v2/message/types"
)

// TransferResponse1_1 is a private struct that satisfies the datatransfer.Response interface
// It is the response message for the Data Transfer 1.1 and 1.2 Protocol.
type TransferResponse1_1 struct {
	MessageType           uint64
	RequestAccepted       bool
	Paused                bool
	TransferId            uint64
	VoucherResultPtr      datamodel.Node
	VoucherTypeIdentifier datatransfer.TypeIdentifier
}

func (trsp *TransferResponse1_1) TransferID() datatransfer.TransferID {
	return datatransfer.TransferID(trsp.TransferId)
}

// IsRequest always returns false in this case because this is a transfer response
func (trsp *TransferResponse1_1) IsRequest() bool {
	return false
}

// IsNew returns true if this is the first response sent
func (trsp *TransferResponse1_1) IsNew() bool {
	return trsp.MessageType == uint64(types.NewMessage)
}

// IsUpdate returns true if this response is an update
func (trsp *TransferResponse1_1) IsUpdate() bool {
	return trsp.MessageType == uint64(types.UpdateMessage)
}

// IsPaused returns true if the responder is paused
func (trsp *TransferResponse1_1) IsPaused() bool {
	return trsp.Paused
}

// IsCancel returns true if the responder has cancelled this response
func (trsp *TransferResponse1_1) IsCancel() bool {
	return trsp.MessageType == uint64(types.CancelMessage)
}

// IsComplete returns true if the responder has completed this response
func (trsp *TransferResponse1_1) IsComplete() bool {
	return trsp.MessageType == uint64(types.CompleteMessage)
}

func (trsp *TransferResponse1_1) IsValidationResult() bool {
	return trsp.MessageType == uint64(types.VoucherResultMessage) || trsp.MessageType == uint64(types.NewMessage) || trsp.MessageType == uint64(types.CompleteMessage) ||
		trsp.MessageType == uint64(types.RestartMessage)
}

// 	Accepted returns true if the request is accepted in the response
func (trsp *TransferResponse1_1) Accepted() bool {
	return trsp.RequestAccepted
}

func (trsp *TransferResponse1_1) VoucherResultType() datatransfer.TypeIdentifier {
	return trsp.VoucherTypeIdentifier
}

func (trsp *TransferResponse1_1) VoucherResult() (datamodel.Node, error) {
	if trsp.VoucherResultPtr == nil {
		return nil, xerrors.New("No voucher present to read")
	}
	return trsp.VoucherResultPtr, nil
}

func (trq *TransferResponse1_1) IsRestart() bool {
	return trq.MessageType == uint64(types.RestartMessage)
}

func (trsp *TransferResponse1_1) EmptyVoucherResult() bool {
	return trsp.VoucherTypeIdentifier == datatransfer.EmptyTypeIdentifier
}

func (trsp *TransferResponse1_1) MessageForVersion(version datatransfer.MessageVersion) (datatransfer.Message, error) {
	switch version {
	case datatransfer.DataTransfer1_2:
		return trsp, nil
	default:
		return nil, xerrors.Errorf("protocol %s not supported", version)
	}
}

func (trsp *TransferResponse1_1) WrappedForTransport(transportID datatransfer.TransportID) datatransfer.Message {
	return &WrappedTransferRepsponse1_1{trsp, string(transportID)}
}
func (trsp *TransferResponse1_1) toIPLD() (schema.TypedNode, error) {
	msg := TransferMessage1_1{
		IsRequest: false,
		Request:   nil,
		Response:  trsp,
	}
	return msg.toIPLD()
}

func (trsp *TransferResponse1_1) ToIPLD() (datamodel.Node, error) {
	msg, err := trsp.toIPLD()
	if err != nil {
		return nil, err
	}
	return msg.Representation(), nil
}

// ToNet serializes a transfer response.
func (trsp *TransferResponse1_1) ToNet(w io.Writer) error {
	i, err := trsp.toIPLD()
	if err != nil {
		return err
	}
	return ipldutils.NodeToWriter(i, w)
}

// WrappedTransferRepsponse1_1 is used to serialize a response along with a
// transport id
type WrappedTransferRepsponse1_1 struct {
	*TransferResponse1_1
	TransportID string
}

func (trsp *WrappedTransferRepsponse1_1) toIPLD() (schema.TypedNode, error) {
	msg := WrappedTransferMessage1_1{
		TransportID: trsp.TransportID,
		Message: TransferMessage1_1{
			IsRequest: false,
			Request:   nil,
			Response:  trsp.TransferResponse1_1,
		},
	}
	return msg.toIPLD()
}

func (trq *WrappedTransferRepsponse1_1) ToIPLD() (datamodel.Node, error) {
	msg, err := trsp.toIPLD()
	if err != nil {
		return err
	}
	return msg.Representation(), nil
}

func (trq *WrappedTransferRepsponse1_1) ToNet(w io.Writer) error {
	msg, err := trsp.toIPLD()
	if err != nil {
		return err
	}
	return ipldutils.NodeToWriter(i, w)
}

