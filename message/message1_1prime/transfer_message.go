package message1_1

import (
	"bytes"
	"io"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/ipld/go-ipld-prime/codec/dagcbor"
	"github.com/ipld/go-ipld-prime/datamodel"
	basicnode "github.com/ipld/go-ipld-prime/node/basic"
	"github.com/ipld/go-ipld-prime/node/bindnode"
)

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

// ToNet serializes a transfer message type. It is simply a wrapper for MarshalCBOR, to provide
// symmetry with FromNet
func (tm *TransferMessage1_1) ToIPLD() (datamodel.Node, error) {
	buf := new(bytes.Buffer)
	err := tm.ToNet(buf)
	if err != nil {
		return nil, err
	}
	nb := basicnode.Prototype.Any.NewBuilder()
	err = dagcbor.Decode(nb, buf)
	if err != nil {
		return nil, err
	}
	return nb.Build(), nil
}

// ToNet serializes a transfer message type. It is simply a wrapper for MarshalCBOR, to provide
// symmetry with FromNet
func (tm *TransferMessage1_1) ToNet(w io.Writer) error {
	node := bindnode.Wrap(&tm, Prototype.TransferMessage.Type())
	return dagcbor.Encode(node.Representation(), w)
}
