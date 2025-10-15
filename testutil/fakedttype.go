package testutil

import (
	"testing"

	"github.com/ipfs/go-test/random"
	"github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/datamodel"
	"github.com/ipld/go-ipld-prime/fluent/qp"
	basicnode "github.com/ipld/go-ipld-prime/node/basic"
	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
)

const TestVoucherType = datatransfer.TypeIdentifier("TestVoucher")

// AssertTestVoucher asserts that a data transfer requests contains the expected fake data transfer voucher type
func AssertTestVoucher(t *testing.T, request datatransfer.Request, expected datatransfer.TypedVoucher) {
	require.Equal(t, expected.Type, request.VoucherType())
	voucher, err := request.Voucher()
	require.NoError(t, err)
	require.True(t, ipld.DeepEqual(expected.Voucher, voucher))
}

// AssertEqualTestVoucher asserts that two requests have the same fake data transfer voucher
func AssertEqualTestVoucher(t *testing.T, expectedRequest datatransfer.Request, request datatransfer.Request) {
	require.Equal(t, expectedRequest.VoucherType(), request.VoucherType())
	require.Equal(t, TestVoucherType, request.VoucherType())
	expected, err := expectedRequest.Voucher()
	require.NoError(t, err)
	actual, err := request.Voucher()
	require.NoError(t, err)
	require.True(t, ipld.DeepEqual(expected, actual))
}

// AssertTestVoucherResult asserts that a data transfer response contains the expected fake data transfer voucher result type
func AssertTestVoucherResult(t *testing.T, response datatransfer.Response, expected datatransfer.TypedVoucher) {
	require.Equal(t, expected.Type, response.VoucherResultType())
	voucherResult, err := response.VoucherResult()
	require.NoError(t, err)
	require.True(t, ipld.DeepEqual(expected.Voucher, voucherResult))
}

// AssertEqualTestVoucherResult asserts that two responses have the same fake data transfer voucher result
func AssertEqualTestVoucherResult(t *testing.T, expectedResponse datatransfer.Response, response datatransfer.Response) {
	require.Equal(t, expectedResponse.VoucherResultType(), response.VoucherResultType())
	expectedVoucherResult, err := expectedResponse.VoucherResult()
	require.NoError(t, err)
	actualVoucherResult, err := response.VoucherResult()
	require.NoError(t, err)
	require.True(t, ipld.DeepEqual(expectedVoucherResult, actualVoucherResult))
}

// NewTestVoucher returns a fake voucher with random data
func NewTestVoucher() datamodel.Node {
	n, err := qp.BuildList(basicnode.Prototype.Any, 1, func(ma datamodel.ListAssembler) {
		qp.ListEntry(ma, qp.String(string(random.Bytes(100))))
	})
	if err != nil {
		panic(err)
	}
	return n
}

func NewTestTypedVoucher() datatransfer.TypedVoucher {
	return datatransfer.TypedVoucher{Voucher: NewTestVoucher(), Type: TestVoucherType}
}

// NewTestVoucher returns a fake voucher with random data
func NewTestVoucherWith(data string) datamodel.Node {
	n, err := qp.BuildList(basicnode.Prototype.Any, 1, func(ma datamodel.ListAssembler) {
		qp.ListEntry(ma, qp.String(data))
	})
	if err != nil {
		panic(err)
	}
	return n
}

func NewTestTypedVoucherWith(data string) datatransfer.TypedVoucher {
	return datatransfer.TypedVoucher{Voucher: NewTestVoucherWith(data), Type: TestVoucherType}
}
