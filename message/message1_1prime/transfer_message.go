package message1_1

import (
	_ "embed"
	"io"

	"github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/schema"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	ipldutils "github.com/filecoin-project/go-data-transfer/v2/ipldutils"
)

//go:embed schema.ipldsch
var embedSchema []byte

// TransferMessage1_1 is the transfer message for the 1.1 Data Transfer Protocol.
type TransferMessage1_1 struct {
	IsRequest bool

	Request  *TransferRequest1_1
	Response *TransferResponse1_1
}

func (tm *TransferMessage1_1) BindnodeSchema() string {
	return string(embedSchema)
}

// ========= datatransfer.Message interface

// TransferID returns the TransferID of this message
func (tm *TransferMessage1_1) TransferID() datatransfer.TransferID {
	if tm.IsRequest {
		return tm.Request.TransferID()
	}
	return tm.Response.TransferID()
}

func (tm *TransferMessage1_1) toIPLD() (schema.TypedNode, error) {
	return ipldutils.ToNode(tm)
}

// ToIPLD converts a transfer message type to an ipld Node
func (tm *TransferMessage1_1) ToIPLD() (ipld.Node, error) {
	node, err := tm.toIPLD()
	if err != nil {
		return nil, err
	}
	return node.Representation(), nil
}

// ToNet serializes a transfer message type.
func (tm *TransferMessage1_1) ToNet(w io.Writer) error {
	i, err := tm.toIPLD()
	if err != nil {
		return err
	}
	return ipldutils.NodeToWriter(i, w)
}
