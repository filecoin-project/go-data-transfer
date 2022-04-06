package testutil

import (
	"testing"

	basicnode "github.com/ipld/go-ipld-prime/node/basic"
	"github.com/ipld/go-ipld-prime/traversal/selector/builder"
	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/message"
	"github.com/filecoin-project/go-data-transfer/v2/message/types"
)

// NewDTRequest makes a new DT Request message
func NewDTRequest(t *testing.T, transferID datatransfer.TransferID) datatransfer.Request {
	voucher := NewFakeDTType()
	baseCid := GenerateCids(1)[0]
	selector := builder.NewSelectorSpecBuilder(basicnode.Prototype.Any).Matcher().Node()
	r, err := message.NewRequest(transferID, false, false, voucher.Type(), voucher, baseCid, selector)
	require.NoError(t, err)
	return r
}

// NewDTResponse makes a new DT Request message
func NewDTResponse(t *testing.T, transferID datatransfer.TransferID) datatransfer.Response {
	vresult := NewFakeDTType()
	r, err := message.NewResponse(transferID, types.NewMessage, false, false, vresult.Type(), vresult)
	require.NoError(t, err)
	return r
}
