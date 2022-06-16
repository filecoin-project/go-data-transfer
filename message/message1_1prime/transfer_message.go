package message1_1

import (
	_ "embed"
	"io"

	"github.com/ipld/go-ipld-prime/codec/dagcbor"
	"github.com/ipld/go-ipld-prime/datamodel"
	bindnoderegistry "github.com/ipld/go-ipld-prime/node/bindnode/registry"
	"github.com/ipld/go-ipld-prime/schema"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
)

var bindnodeRegistry = bindnoderegistry.NewRegistry()

//go:embed schema.ipldsch
var embedSchema []byte

// TransferMessage1_1 is the transfer message for the 1.1 Data Transfer Protocol.
type TransferMessage1_1 struct {
	IsRequest bool

	Request  *TransferRequest1_1
	Response *TransferResponse1_1
}

// ========= datatransfer.Message interface

// TransferID returns the TransferID of this message
func (tm *TransferMessage1_1) TransferID() datatransfer.TransferID {
	if tm.IsRequest {
		return tm.Request.TransferID()
	}
	return tm.Response.TransferID()
}

func (tm *TransferMessage1_1) toIPLD() schema.TypedNode {
	return bindnodeRegistry.TypeToNode(tm)
}

// ToIPLD converts a transfer message type to an ipld Node
func (tm *TransferMessage1_1) ToIPLD() (datamodel.Node, error) {
	return tm.toIPLD().Representation(), nil
}

// ToNet serializes a transfer message type.
func (tm *TransferMessage1_1) ToNet(w io.Writer) error {
	return bindnodeRegistry.TypeToWriter(tm.toIPLD(), w, dagcbor.Encode)
}

func init() {
	if err := bindnodeRegistry.RegisterType((*TransferMessage1_1)(nil), string(embedSchema), "TransferMessage1_1"); err != nil {
		panic(err.Error())
	}
	if err := bindnodeRegistry.RegisterType((*WrappedTransferMessage1_1)(nil), string(embedSchema), "WrappedTransferMessage1_1"); err != nil {
		panic(err.Error())
	}
}

type WrappedTransferMessage1_1 struct {
	TransportID string
	Message     TransferMessage1_1
}

func (wtm *WrappedTransferMessage1_1) BindnodeSchema() string {
	return string(embedSchema)
}

func (wtm *WrappedTransferMessage1_1) toIPLD() schema.TypedNode {
	return bindnodeRegistry.TypeToNode(wtm)
}
