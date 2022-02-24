package message1_1

import "github.com/filecoin-project/go-data-transfer/ipldbind"

func init() {
	ipldbind.RegisterType((*TransferMessage1_1)(nil))
	ipldbind.RegisterType((*TransferRequest1_1)(nil))
	ipldbind.RegisterType((*TransferResponse1_1)(nil))
}
