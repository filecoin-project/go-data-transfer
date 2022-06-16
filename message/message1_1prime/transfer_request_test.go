package message1_1_test

import (
	"math/rand"
	"testing"

	basicnode "github.com/ipld/go-ipld-prime/node/basic"
	"github.com/ipld/go-ipld-prime/traversal/selector/builder"
	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	message1_1 "github.com/filecoin-project/go-data-transfer/v2/message/message1_1prime"
	"github.com/filecoin-project/go-data-transfer/v2/testutil"
)

func TestRequestMessageForVersion(t *testing.T) {
	baseCid := testutil.GenerateCids(1)[0]
	selector := builder.NewSelectorSpecBuilder(basicnode.Prototype.Any).Matcher().Node()
	isPull := true
	id := datatransfer.TransferID(rand.Int31())
	voucher := testutil.NewTestTypedVoucher()

	// for the new protocols
	request, err := message1_1.NewRequest(id, false, isPull, &voucher, baseCid, selector)
	require.NoError(t, err)

	// v1.2 new protocol
	out12, err := request.MessageForVersion(datatransfer.DataTransfer1_2)
	require.NoError(t, err)
	require.Equal(t, request, out12)

	req, ok := out12.(datatransfer.Request)
	require.True(t, ok)
	require.False(t, req.IsRestart())
	require.False(t, req.IsRestartExistingChannelRequest())
	require.Equal(t, baseCid, req.BaseCid())
	require.True(t, req.IsPull())
	n, err := req.Selector()
	require.NoError(t, err)
	require.Equal(t, selector, n)
	require.Equal(t, testutil.TestVoucherType, req.VoucherType())

	wrappedOut12 := out12.WrappedForTransport(datatransfer.LegacyTransportID, datatransfer.LegacyTransportVersion)
	require.Equal(t, datatransfer.LegacyTransportID, wrappedOut12.TransportID())
	require.Equal(t, datatransfer.LegacyTransportVersion, wrappedOut12.TransportVersion())

	// random protocol should fail
	_, err = request.MessageForVersion(datatransfer.Version{
		Major: rand.Uint64(),
		Minor: rand.Uint64(),
		Patch: rand.Uint64(),
	})
	require.Error(t, err)

}
