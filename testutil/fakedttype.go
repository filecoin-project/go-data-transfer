package testutil

import (
	"testing"

	"github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/schema"
	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/ipldutil"
)

// FakeDTType simple fake type for using with registries
type FakeDTType struct {
	Data string
}

type FakeDTTypeWithEncoded struct {
	FakeDTType *FakeDTType
	Encoded    ipld.Node
}

const FakeDTVoucherType = datatransfer.TypeIdentifier("FakeDTType")

func init() {
	ipldutil.RegisterType(string(testingSchema), (*FakeDTType)(nil))
}

// AssertFakeDTVoucher asserts that a data transfer requests contains the expected fake data transfer voucher type
func AssertFakeDTVoucher(t *testing.T, request datatransfer.Request, expected datatransfer.Voucher) {
	require.Equal(t, FakeDTVoucherType, request.VoucherType())
	voucher, err := request.Voucher()
	require.NoError(t, err)
	if tn, ok := voucher.(schema.TypedNode); ok {
		voucher = tn.Representation()
	}
	if tn, ok := expected.(schema.TypedNode); ok {
		expected = tn.Representation()
	}
	require.True(t, ipld.DeepEqual(expected, voucher))
}

// AssertEqualFakeDTVoucher asserts that two requests have the same fake data transfer voucher
func AssertEqualFakeDTVoucher(t *testing.T, expectedRequest datatransfer.Request, request datatransfer.Request) {
	require.Equal(t, expectedRequest.VoucherType(), request.VoucherType())
	expectedNode, err := expectedRequest.Voucher()
	require.NoError(t, err)
	expectedDecoded, err := ipldutil.FromNode(expectedNode, &FakeDTType{})
	require.NoError(t, err)
	node, err := request.Voucher()
	require.NoError(t, err)
	decoded, err := ipldutil.FromNode(node, &FakeDTType{})
	require.NoError(t, err)
	require.Equal(t, expectedDecoded, decoded)
}

// AssertFakeDTVoucherResult asserts that a data transfer response contains the expected fake data transfer voucher result type
func AssertFakeDTVoucherResult(t *testing.T, response datatransfer.Response, expected ipld.Node) {
	require.Equal(t, datatransfer.TypeIdentifier("FakeDTType"), response.VoucherResultType())
	node, err := response.VoucherResult()
	require.NoError(t, err)
	if tn, ok := node.(schema.TypedNode); ok {
		node = tn.Representation()
	}
	if tn, ok := expected.(schema.TypedNode); ok {
		expected = tn.Representation()
	}
	require.True(t, ipld.DeepEqual(expected, node))
}

// AssertEqualFakeDTVoucherResult asserts that two responses have the same fake data transfer voucher result
func AssertEqualFakeDTVoucherResult(t *testing.T, expectedResponse datatransfer.Response, response datatransfer.Response) {
	require.Equal(t, expectedResponse.VoucherResultType(), response.VoucherResultType())
	expectedNode, err := expectedResponse.VoucherResult()
	require.NoError(t, err)
	expectedDecoded, err := ipldutil.FromNode(expectedNode, &FakeDTType{})
	require.NoError(t, err)
	node, err := response.VoucherResult()
	require.NoError(t, err)
	decoded, err := ipldutil.FromNode(node, &FakeDTType{})
	require.NoError(t, err)
	require.Equal(t, expectedDecoded, decoded)
}

// NewFakeDTType returns a fake dt type with random data
func NewFakeDTType() *FakeDTType {
	return &FakeDTType{Data: string(RandomBytes(100))}
}

func NewFakeDTTypeNode() ipld.Node {
	node, err := ipldutil.ToNode(NewFakeDTType())
	if err != nil {
		panic(err)
	}
	return node
}

func NewFakeDTTypeNodeWithData(data string) ipld.Node {
	node, err := ipldutil.ToNode(&FakeDTType{Data: data})
	if err != nil {
		panic(err)
	}
	return node
}

func NewFakeDTTypeWithEncoded() FakeDTTypeWithEncoded {
	fd := NewFakeDTType()
	node, err := ipldutil.ToNode(fd)
	if err != nil {
		panic(err)
	}
	return FakeDTTypeWithEncoded{fd, node}
}
