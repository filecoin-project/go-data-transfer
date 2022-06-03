package message1_1

import (
	"io"

	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime/datamodel"
	"github.com/ipld/go-ipld-prime/schema"
	xerrors "golang.org/x/xerrors"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	ipldutils "github.com/filecoin-project/go-data-transfer/v2/ipldutils"
	"github.com/filecoin-project/go-data-transfer/v2/message/types"
)

// TransferRequest1_1 is a struct for the 1.1 Data Transfer Protocol that fulfills the datatransfer.Request interface.
// its members are exported to be used by cbor-gen
type TransferRequest1_1 struct {
	BaseCidPtr            *cid.Cid
	MessageType           uint64
	Pause                 bool
	Partial               bool
	Pull                  bool
	SelectorPtr           datamodel.Node
	VoucherPtr            datamodel.Node
	VoucherTypeIdentifier datatransfer.TypeIdentifier
	TransferId            uint64
	RestartChannel        datatransfer.ChannelID
}

func (trq *TransferRequest1_1) MessageForVersion(version datatransfer.Version) (datatransfer.Message, error) {
	switch version {
	case datatransfer.DataTransfer1_2:
		return trq, nil
	default:
		return nil, xerrors.Errorf("protocol not supported")
	}
}

func (trq *TransferRequest1_1) Version() datatransfer.Version {
	return datatransfer.DataTransfer1_2
}

func (trq *TransferRequest1_1) WrappedForTransport(transportID datatransfer.TransportID, transportVersion datatransfer.Version) datatransfer.TransportedMessage {
	return &WrappedTransferRequest1_1{
		TransferRequest1_1: trq,
		transportID:        string(transportID),
		transportVersion:   transportVersion,
	}
}

// IsRequest always returns true in this case because this is a transfer request
func (trq *TransferRequest1_1) IsRequest() bool {
	return true
}

func (trq *TransferRequest1_1) IsRestart() bool {
	return trq.MessageType == uint64(types.RestartMessage)
}

func (trq *TransferRequest1_1) IsRestartExistingChannelRequest() bool {
	return trq.MessageType == uint64(types.RestartExistingChannelRequestMessage)
}

func (trq *TransferRequest1_1) RestartChannelId() (datatransfer.ChannelID, error) {
	if !trq.IsRestartExistingChannelRequest() {
		return datatransfer.ChannelID{}, xerrors.New("not a restart request")
	}
	return trq.RestartChannel, nil
}

func (trq *TransferRequest1_1) IsNew() bool {
	return trq.MessageType == uint64(types.NewMessage)
}

func (trq *TransferRequest1_1) IsUpdate() bool {
	return trq.MessageType == uint64(types.UpdateMessage)
}

func (trq *TransferRequest1_1) IsVoucher() bool {
	return trq.MessageType == uint64(types.VoucherMessage) || trq.MessageType == uint64(types.NewMessage)
}

func (trq *TransferRequest1_1) IsPaused() bool {
	return trq.Pause
}

func (trq *TransferRequest1_1) TransferID() datatransfer.TransferID {
	return datatransfer.TransferID(trq.TransferId)
}

// ========= datatransfer.Request interface
// IsPull returns true if this is a data pull request
func (trq *TransferRequest1_1) IsPull() bool {
	return trq.Pull
}

// VoucherType returns the Voucher ID
func (trq *TransferRequest1_1) VoucherType() datatransfer.TypeIdentifier {
	return trq.VoucherTypeIdentifier
}

// Voucher returns the Voucher bytes
func (trq *TransferRequest1_1) Voucher() (datamodel.Node, error) {
	if trq.VoucherPtr == nil {
		return nil, xerrors.New("No voucher present to read")
	}
	return trq.VoucherPtr, nil
}

// TypedVoucher is a convenience method that returns the voucher and its typed
// as a TypedVoucher object
// TODO(rvagg): tests for this
func (trq *TransferRequest1_1) TypedVoucher() (datatransfer.TypedVoucher, error) {
	voucher, err := trq.Voucher()
	if err != nil {
		return datatransfer.TypedVoucher{}, err
	}
	return datatransfer.TypedVoucher{Voucher: voucher, Type: trq.VoucherType()}, nil
}

func (trq *TransferRequest1_1) EmptyVoucher() bool {
	return trq.VoucherTypeIdentifier == datatransfer.EmptyTypeIdentifier
}

// BaseCid returns the Base CID
func (trq *TransferRequest1_1) BaseCid() cid.Cid {
	if trq.BaseCidPtr == nil {
		return cid.Undef
	}
	return *trq.BaseCidPtr
}

// Selector returns the message Selector bytes
func (trq *TransferRequest1_1) Selector() (datamodel.Node, error) {
	if trq.SelectorPtr == nil {
		return nil, xerrors.New("No selector present to read")
	}
	return trq.SelectorPtr, nil
}

// IsCancel returns true if this is a cancel request
func (trq *TransferRequest1_1) IsCancel() bool {
	return trq.MessageType == uint64(types.CancelMessage)
}

// IsPartial returns true if this is a partial request
func (trq *TransferRequest1_1) IsPartial() bool {
	return trq.Partial
}

func (trq *TransferRequest1_1) toIPLD() (schema.TypedNode, error) {
	msg := TransferMessage1_1{
		IsRequest: true,
		Request:   trq,
		Response:  nil,
	}
	return msg.toIPLD()
}

func (trq *TransferRequest1_1) ToIPLD() (datamodel.Node, error) {
	msg, err := trq.toIPLD()
	if err != nil {
		return nil, err
	}
	return msg.Representation(), nil
}

// ToNet serializes a transfer request.
func (trq *TransferRequest1_1) ToNet(w io.Writer) error {
	i, err := trq.toIPLD()
	if err != nil {
		return err
	}
	return ipldutils.NodeToWriter(i, w)
}

// WrappedTransferRequest1_1 is used to serialize a request along with a
// transport id
type WrappedTransferRequest1_1 struct {
	*TransferRequest1_1
	transportVersion datatransfer.Version
	transportID      string
}

func (trq *WrappedTransferRequest1_1) TransportID() datatransfer.TransportID {
	return datatransfer.TransportID(trq.transportID)
}

func (trq *WrappedTransferRequest1_1) TransportVersion() datatransfer.Version {
	return trq.transportVersion
}

func (trq *WrappedTransferRequest1_1) toIPLD() (schema.TypedNode, error) {
	msg := WrappedTransferMessage1_1{
		TransportID:      trq.transportID,
		TransportVersion: trq.transportVersion,
		Message: TransferMessage1_1{
			IsRequest: true,
			Request:   trq.TransferRequest1_1,
			Response:  nil,
		},
	}
	return msg.toIPLD()
}

func (trq *WrappedTransferRequest1_1) ToIPLD() (datamodel.Node, error) {
	msg, err := trq.toIPLD()
	if err != nil {
		return nil, err
	}
	return msg.Representation(), nil
}

func (trq *WrappedTransferRequest1_1) ToNet(w io.Writer) error {
	i, err := trq.toIPLD()
	if err != nil {
		return err
	}
	return ipldutils.NodeToWriter(i, w)
}
