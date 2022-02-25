package testutil

import (
	"testing"

	basicnode "github.com/ipld/go-ipld-prime/node/basic"
	"github.com/ipld/go-ipld-prime/traversal/selector/builder"
	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/ipldutil"
	"github.com/filecoin-project/go-data-transfer/message"
)

// NewDTRequest makes a new DT Request message
func NewDTRequest(t *testing.T, transferID datatransfer.TransferID) datatransfer.Request {
	node, err := ipldutil.ToNode(NewFakeDTType())
	require.NoError(t, err)
	voucher := datatransfer.Voucher(node)
	baseCid := GenerateCids(1)[0]
	selector := builder.NewSelectorSpecBuilder(basicnode.Prototype.Any).Matcher().Node()
	r, err := message.NewRequest(transferID, false, false, FakeDTVoucherType, voucher, baseCid, selector)
	require.NoError(t, err)
	return r
}

// NewDTResponse makes a new DT Request message
func NewDTResponse(t *testing.T, transferID datatransfer.TransferID) datatransfer.Response {
	vresult := NewFakeDTTypeNode()
	r, err := message.NewResponse(transferID, false, false, FakeDTVoucherType, vresult)
	require.NoError(t, err)
	return r
}
