package message1_1_test

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	message1_1 "github.com/filecoin-project/go-data-transfer/v2/message/message1_1prime"
	"github.com/filecoin-project/go-data-transfer/v2/testutil"
)

func TestResponseMessageForVersion(t *testing.T) {
	id := datatransfer.TransferID(rand.Int31())
	voucherResult := testutil.NewTestTypedVoucher()
	response, err := message1_1.NewResponse(id, false, true, &voucherResult) // not accepted
	require.NoError(t, err)

	// v1.2 new protocol
	out, err := response.MessageForVersion(datatransfer.DataTransfer1_2)
	require.NoError(t, err)
	require.Equal(t, response, out)

	resp, ok := (out).(datatransfer.Response)
	require.True(t, ok)
	require.True(t, resp.IsPaused())
	require.Equal(t, testutil.TestVoucherType, resp.VoucherResultType())
	require.True(t, resp.IsValidationResult())

	wrappedOut := out.WrappedForTransport(datatransfer.LegacyTransportID)
	require.Equal(t, &message1_1.WrappedTransferResponse1_1{
		TransferResponse1_1: response.(*message1_1.TransferResponse1_1),
		TransportID:         string(datatransfer.LegacyTransportID),
	}, wrappedOut)

	// random protocol should fail
	_, err = response.MessageForVersion(datatransfer.MessageVersion{
		Major: rand.Uint64(),
		Minor: rand.Uint64(),
		Patch: rand.Uint64(),
	})
	require.Error(t, err)
}
