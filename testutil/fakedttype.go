package testutil

import (
	"testing"

	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/ipldbind"
)

// FakeDTType simple fake type for using with registries
type FakeDTType struct {
	Data string
}

// Type satisfies registry.Entry
func (ft FakeDTType) Type() datatransfer.TypeIdentifier {
	return "FakeDTType"
}

func init() {
	ipldbind.RegisterType(string(testingSchema), (*FakeDTType)(nil))
}

// AssertFakeDTVoucher asserts that a data transfer requests contains the expected fake data transfer voucher type
func AssertFakeDTVoucher(t *testing.T, request datatransfer.Request, expected *FakeDTType) {
	require.Equal(t, datatransfer.TypeIdentifier("FakeDTType"), request.VoucherType())
	decoded, err := request.Voucher(&FakeDTType{})
	require.NoError(t, err)
	require.Equal(t, expected, decoded)
}

// AssertEqualFakeDTVoucher asserts that two requests have the same fake data transfer voucher
func AssertEqualFakeDTVoucher(t *testing.T, expectedRequest datatransfer.Request, request datatransfer.Request) {
	require.Equal(t, expectedRequest.VoucherType(), request.VoucherType())
	expectedDecoded, err := expectedRequest.Voucher(&FakeDTType{})
	require.NoError(t, err)
	decoded, err := request.Voucher(&FakeDTType{})
	require.NoError(t, err)
	require.Equal(t, expectedDecoded, decoded)
}

// AssertFakeDTVoucherResult asserts that a data transfer response contains the expected fake data transfer voucher result type
func AssertFakeDTVoucherResult(t *testing.T, response datatransfer.Response, expected *FakeDTType) {
	require.Equal(t, datatransfer.TypeIdentifier("FakeDTType"), response.VoucherResultType())
	decoded, err := response.VoucherResult(&FakeDTType{})
	require.NoError(t, err)
	require.Equal(t, expected, decoded)
}

// AssertEqualFakeDTVoucherResult asserts that two responses have the same fake data transfer voucher result
func AssertEqualFakeDTVoucherResult(t *testing.T, expectedResponse datatransfer.Response, response datatransfer.Response) {
	require.Equal(t, expectedResponse.VoucherResultType(), response.VoucherResultType())
	expectedDecoded, err := response.VoucherResult(&FakeDTType{})
	require.NoError(t, err)
	decoded, err := response.VoucherResult(&FakeDTType{})
	require.NoError(t, err)
	require.Equal(t, expectedDecoded, decoded)
}

// NewFakeDTType returns a fake dt type with random data
func NewFakeDTType() *FakeDTType {
	return &FakeDTType{Data: string(RandomBytes(100))}
}

var _ datatransfer.Registerable = &FakeDTType{}
