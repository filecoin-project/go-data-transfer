package message1_1_test

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/ipldutil"
	message1_1 "github.com/filecoin-project/go-data-transfer/message/message1_1prime"
	"github.com/filecoin-project/go-data-transfer/testutil"
)

func TestResponseMessageForProtocol(t *testing.T) {
	id := datatransfer.TransferID(rand.Int31())
	node, err := ipldutil.ToNode(testutil.NewFakeDTType())
	require.NoError(t, err)
	voucherResult := datatransfer.VoucherResult(node)
	response, err := message1_1.NewResponse(id, false, true, testutil.FakeDTVoucherType, voucherResult) // not accepted
	require.NoError(t, err)

	// v1.2 protocol
	out, err := response.MessageForProtocol(datatransfer.ProtocolDataTransfer1_2)
	require.NoError(t, err)
	require.Equal(t, response, out)

	resp, ok := (out).(datatransfer.Response)
	require.True(t, ok)
	require.True(t, resp.IsPaused())
	require.Equal(t, testutil.FakeDTVoucherType, resp.VoucherResultType())
	require.True(t, resp.IsVoucherResult())

	// random protocol
	out, err = response.MessageForProtocol("RAND")
	require.Error(t, err)
	require.Nil(t, out)
}
