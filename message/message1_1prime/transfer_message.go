package message1_1

import (
	"io"

	"github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/codec/dagcbor"
	"github.com/ipld/go-ipld-prime/schema"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/ipldutil"
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

func (tm *TransferMessage1_1) toIPLD() schema.TypedNode {
	tn, _ := ipldutil.ToNode(&tm) // should never error for this
	return tn.(schema.TypedNode)
}

// ToIPLD converts a TransferMessage into a Node
func (tm *TransferMessage1_1) ToIPLD() (ipld.Node, error) {
	return tm.toIPLD().Representation(), nil
}

// ToNet serializes a transfer message type.
func (tm *TransferMessage1_1) ToNet(w io.Writer) error {
	return dagcbor.Encode(tm.toIPLD().Representation(), w)
}
