package message1_1

import (
	"fmt"
	"io"
	"os"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/ipld/go-ipld-prime/codec/dagcbor"
	"github.com/ipld/go-ipld-prime/codec/dagjson"
	"github.com/ipld/go-ipld-prime/datamodel"
	"github.com/ipld/go-ipld-prime/node/bindnode"
	"github.com/ipld/go-ipld-prime/schema"
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
	return bindnode.Wrap(&tm, Prototype.TransferMessage.Type())
}

// ToNet serializes a transfer message type.
func (tm *TransferMessage1_1) ToIPLD() (datamodel.Node, error) {
	fmt.Printf("ToIPLD: ")
	dagjson.Encode(tm.toIPLD().Representation(), os.Stdout)
	fmt.Println()
	return tm.toIPLD().Representation(), nil
}

// ToNet serializes a transfer message type.
func (tm *TransferMessage1_1) ToNet(w io.Writer) error {
	fmt.Printf("ToNet: ")
	dagjson.Encode(tm.toIPLD().Representation(), os.Stdout)
	fmt.Println()
	return dagcbor.Encode(tm.toIPLD().Representation(), w)
}
