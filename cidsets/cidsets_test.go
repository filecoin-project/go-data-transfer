package cidsets

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/filecoin-project/go-data-transfer/testutil"
	ds "github.com/ipfs/go-datastore"
	ds_sync "github.com/ipfs/go-datastore/sync"
)

func TestCIDSetManager(t *testing.T) {
	cid1 := testutil.GenerateCids(1)[0]

	dstore := ds_sync.MutexWrap(ds.NewMapDatastore())
	mgr := NewCIDSetManager(dstore)
	setID1 := SetID("set1")
	setID2 := SetID("set2")

	exists, err := mgr.InsertSetCID(setID1, cid1)
	require.NoError(t, err)
	require.False(t, exists)

	exists, err = mgr.InsertSetCID(setID1, cid1)
	require.NoError(t, err)
	require.True(t, exists)

	exists, err = mgr.InsertSetCID(setID2, cid1)
	require.NoError(t, err)
	require.False(t, exists)

	exists, err = mgr.InsertSetCID(setID2, cid1)
	require.NoError(t, err)
	require.True(t, exists)

	err = mgr.DeleteSet(setID1)
	require.NoError(t, err)

	exists, err = mgr.InsertSetCID(setID1, cid1)
	require.NoError(t, err)
	require.False(t, exists)

	exists, err = mgr.InsertSetCID(setID2, cid1)
	require.NoError(t, err)
	require.True(t, exists)
}
