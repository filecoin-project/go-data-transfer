package testutil

import (
	"testing"

	basicnode "github.com/ipld/go-ipld-prime/node/basic"
	"github.com/ipld/go-ipld-prime/traversal/selector/builder"
	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/message"
)

// NewDTRequest makes a new DT Request message
func NewDTRequest(t *testing.T, transferID datatransfer.TransferID) datatransfer.Request {
	voucher := NewTestVoucher()
	baseCid := GenerateCids(1)[0]
	selector := builder.NewSelectorSpecBuilder(basicnode.Prototype.Any).Matcher().Node()
	r, err := message.NewRequest(transferID, false, false, TestVoucherType, voucher, baseCid, selector)
	require.NoError(t, err)
	return r
}

// NewDTResponse makes a new DT Request message
func NewDTResponse(t *testing.T, transferID datatransfer.TransferID) datatransfer.Response {
	vresult := NewTestVoucher()
	r, err := message.NewResponse(transferID, false, false, TestVoucherType, vresult)
	require.NoError(t, err)
	return r
}
