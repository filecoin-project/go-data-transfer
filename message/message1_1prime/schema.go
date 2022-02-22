package message1_1

import (
	_ "embed"

	"github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/node/bindnode"
	"github.com/ipld/go-ipld-prime/schema"
)

//go:embed schema.ipldsch
var embedSchema []byte

var Prototype struct {
	TransferMessage  schema.TypedPrototype
	TransferRequest  schema.TypedPrototype
	TransferResponse schema.TypedPrototype
}

func init() {
	ts, err := ipld.LoadSchemaBytes(embedSchema)
	if err != nil {
		panic(err)
	}

	Prototype.TransferMessage = bindnode.Prototype((*transferMessage1_1)(nil), ts.TypeByName("TransferMessage"))
	Prototype.TransferRequest = bindnode.Prototype((*transferRequest1_1)(nil), ts.TypeByName("TransferRequest"))
	Prototype.TransferResponse = bindnode.Prototype((*transferResponse1_1)(nil), ts.TypeByName("TransferResponse"))
}
