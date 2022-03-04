package message1_1

import (
	"io"

	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/codec/dagcbor"
	"github.com/ipld/go-ipld-prime/datamodel"
	"github.com/ipld/go-ipld-prime/node/bindnode"
	"github.com/ipld/go-ipld-prime/schema"
	peer "github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-core/protocol"
	xerrors "golang.org/x/xerrors"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/message/types"
)

type channelID struct {
	Initiator peer.ID
	Responder peer.ID
	ID        int64
}

// transferRequest1_1 is a struct for the 1.1 Data Transfer Protocol that fulfills the datatransfer.Request interface.
// its members are exported to be used by cbor-gen
type transferRequest1_1 struct {
	BCid   *cid.Cid
	Type   int64
	Paus   bool
	Part   bool
	Pull   bool
	Stor   datamodel.Node
	Vouch  datamodel.Node
	VTyp   schema.TypeName
	XferID int64

	RestartChannel channelID
}

func (trq *transferRequest1_1) MessageForProtocol(targetProtocol protocol.ID) (datatransfer.Message, error) {
	switch targetProtocol {
	case datatransfer.ProtocolDataTransfer1_2:
		return trq, nil
	default:
		return nil, xerrors.Errorf("protocol not supported")
	}
}

// IsRequest always returns true in this case because this is a transfer request
func (trq *transferRequest1_1) IsRequest() bool {
	return true
}

func (trq *transferRequest1_1) IsRestart() bool {
	return trq.Type == int64(types.RestartMessage)
}

func (trq *transferRequest1_1) IsRestartExistingChannelRequest() bool {
	return trq.Type == int64(types.RestartExistingChannelRequestMessage)
}

func (trq *transferRequest1_1) RestartChannelId() (datatransfer.ChannelID, error) {
	if !trq.IsRestartExistingChannelRequest() {
		return datatransfer.ChannelID{}, xerrors.New("not a restart request")
	}
	return datatransfer.ChannelID{
		Initiator: trq.RestartChannel.Initiator,
		Responder: trq.RestartChannel.Responder,
		ID:        datatransfer.TransferID(trq.RestartChannel.ID),
	}, nil
}

func (trq *transferRequest1_1) IsNew() bool {
	return trq.Type == int64(types.NewMessage)
}

func (trq *transferRequest1_1) IsUpdate() bool {
	return trq.Type == int64(types.UpdateMessage)
}

func (trq *transferRequest1_1) IsVoucher() bool {
	return trq.Type == int64(types.VoucherMessage) || trq.Type == int64(types.NewMessage)
}

func (trq *transferRequest1_1) IsPaused() bool {
	return trq.Paus
}

func (trq *transferRequest1_1) TransferID() datatransfer.TransferID {
	return datatransfer.TransferID(trq.XferID)
}

// ========= datatransfer.Request interface
// IsPull returns true if this is a data pull request
func (trq *transferRequest1_1) IsPull() bool {
	return trq.Pull
}

// VoucherType returns the Voucher ID
func (trq *transferRequest1_1) VoucherType() schema.TypeName {
	return trq.VTyp
}

// Voucher returns the Voucher bytes
func (trq *transferRequest1_1) Voucher(proto datamodel.NodePrototype) (datamodel.Node, error) {
	if trq.Vouch == nil {
		return nil, xerrors.New("No voucher present to read")
	}
	nb := proto.NewBuilder()
	err := nb.AssignNode(trq.Vouch)
	if err != nil {
		return nil, err
	}
	return nb.Build(), nil
}

func (trq *transferRequest1_1) EmptyVoucher() bool {
	return trq.VTyp == datatransfer.EmptyTypeIdentifier
}

// BaseCid returns the Base CID
func (trq *transferRequest1_1) BaseCid() cid.Cid {
	if trq.BCid == nil {
		return cid.Undef
	}
	return *trq.BCid
}

// Selector returns the message Selector bytes
func (trq *transferRequest1_1) Selector() (ipld.Node, error) {
	if trq.Stor == nil {
		return nil, xerrors.New("No selector present to read")
	}
	return trq.Stor, nil
}

// IsCancel returns true if this is a cancel request
func (trq *transferRequest1_1) IsCancel() bool {
	return trq.Type == int64(types.CancelMessage)
}

// IsPartial returns true if this is a partial request
func (trq *transferRequest1_1) IsPartial() bool {
	return trq.Part
}

func (trq *transferRequest1_1) AsNode() schema.TypedNode {
	msg := &transferMessage1_1{
		IsRq:     true,
		Request:  trq,
		Response: nil,
	}
	return bindnode.Wrap(msg, Prototype.Message.Type())
}

// ToNet serializes a transfer request. It's a wrapper for MarshalCBOR to provide
// symmetry with FromNet
func (trq *transferRequest1_1) ToNet(w io.Writer) error {
	return dagcbor.Encode(trq.AsNode().Representation(), w)
}
