package message1_1

import (
	_ "embed"
	"strings"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/ipldbind"
)

//go:embed message.ipldsch
var messageSchema []byte

func init() {
	schema := strings.Join([]string{string(datatransfer.TypesSchema), string(messageSchema)}, "")

	ipldbind.RegisterType(schema, (*TransferMessage1_1)(nil))
	ipldbind.RegisterType(schema, (*TransferRequest1_1)(nil))
	ipldbind.RegisterType(schema, (*TransferResponse1_1)(nil))
}
