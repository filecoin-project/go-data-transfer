package ipldutil_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/ipld/go-ipld-prime/datamodel"
	"github.com/ipld/go-ipld-prime/node/basicnode"
	"github.com/stretchr/testify/require"

	"github.com/filecoin-project/go-data-transfer/ipldutil"
	"github.com/filecoin-project/go-data-transfer/testutil"
)

func TestConversion(t *testing.T) {
	fd := testutil.NewFakeDTType() // using testutil will trigger a Register() of FakeDTType
	node, err := ipldutil.ToNode(fd)
	require.NoError(t, err)
	require.Equal(t, datamodel.Kind_Map, node.Kind())
	d, err := node.LookupByString("Data")
	require.NoError(t, err)
	dstr, err := d.AsString()
	require.NoError(t, err)
	require.Equal(t, fd.Data, dstr)

	rt, err := ipldutil.FromNode(node, &testutil.FakeDTType{})
	require.NoError(t, err)
	require.Equal(t, fd, rt)

	cbor, err := ipldutil.ToDagCbor(fd)
	require.NoError(t, err)
	require.Equal(t, append([]byte{ /*array(1)*/ 0x81 /*string(86)*/, 0x78, 0x64}, []byte(fd.Data)...), cbor)

	encRt, err := ipldutil.FromDagCbor(bytes.NewReader(cbor), (*testutil.FakeDTType)(nil))
	require.NoError(t, err)
	require.Equal(t, fd, encRt)

	nodeRt, err := ipldutil.NodeFromDagCbor(bytes.NewReader(cbor))
	require.NoError(t, err)
	require.Equal(t, datamodel.Kind_List, nodeRt.Kind())
	d, err = nodeRt.LookupByIndex(0)
	require.NoError(t, err)
	dstr, err = d.AsString()
	require.NoError(t, err)
	require.Equal(t, fd.Data, dstr)
}

func TestNilNode(t *testing.T) {
	node, err := ipldutil.ToNode(nil)
	require.NoError(t, err)
	require.Equal(t, datamodel.Null, node)
	byts, err := ipldutil.ToDagCbor(nil)
	require.NoError(t, err)
	require.Equal(t, []byte{0xf6}, byts)
}

func TestNodeToNode(t *testing.T) {
	expected := basicnode.NewString("BlipBlop")
	actual, err := ipldutil.ToNode(expected)
	require.NoError(t, err)
	require.Equal(t, expected, actual)
	byts, err := ipldutil.ToDagCbor(expected)
	require.NoError(t, err)
	require.Equal(t, append([]byte{0x68}, []byte("BlipBlop")...), byts)
}

func TestUnknownType(t *testing.T) {
	type UnknownType struct{}
	nope, err := ipldutil.ToNode(&UnknownType{})
	require.EqualError(t, err, "invalid TypedPrototype: UnknownType")
	require.Nil(t, nope)

	byts, err := ipldutil.ToDagCbor(&UnknownType{})
	require.EqualError(t, err, "invalid TypedPrototype: UnknownType")
	require.Nil(t, byts)

	inst, err := ipldutil.FromDagCbor(bytes.NewReader(append([]byte{0x68}, []byte("BlipBlop")...)), &UnknownType{})
	require.EqualError(t, err, "invalid TypedPrototype: UnknownType")
	require.Nil(t, inst)

	inst, err = ipldutil.FromNode(basicnode.NewString("BlipBlop"), &UnknownType{})
	require.EqualError(t, err, "invalid TypedPrototype: UnknownType")
	require.Nil(t, inst)
}

func TestInvalidSchema(t *testing.T) {
	inst, err := ipldutil.FromNode(basicnode.NewString("BlipBlop"), &testutil.FakeDTType{})
	require.Error(t, err) // error should arise from go-ipld-prime
	require.Nil(t, inst)

	inst, err = ipldutil.FromDagCbor(bytes.NewReader(append([]byte{0x68}, []byte("BlipBlop")...)), &testutil.FakeDTType{})
	require.Error(t, err) // error should arise from go-ipld-prime
	require.Nil(t, inst)
}

func TestInvalidCbor(t *testing.T) {
	inst, err := ipldutil.NodeFromDagCbor(bytes.NewReader(append([]byte{0x69}, []byte("BlipBlop")...)))
	require.ErrorIs(t, err, io.ErrUnexpectedEOF) // error should arise from go-ipld-prime
	require.Nil(t, inst)
}
