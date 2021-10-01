package channels

import (
	"testing"

	ds "github.com/ipfs/go-datastore"
	ds_sync "github.com/ipfs/go-datastore/sync"
	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer"
)

// Add items to set then get the length (to make sure that internal caching
// is working correctly)
func TestBlocksReceivedIndex(t *testing.T) {
	dstore := ds_sync.MutexWrap(ds.NewMapDatastore())
	bri := newBlocksReceivedIndex(dstore)
	ch1 := datatransfer.ChannelID{
		ID:        1,
		Initiator: "initiator",
		Responder: "responder",
	}

	// verify get index before setting index returns 0
	idx, err := bri.get(ch1)
	require.NoError(t, err)
	require.EqualValues(t, 0, idx)

	// set index to 10
	err = bri.update(ch1, 10)
	require.NoError(t, err)

	// verify index is 10
	idx, err = bri.get(ch1)
	require.NoError(t, err)
	require.EqualValues(t, 10, idx)

	// set index to 15
	err = bri.update(ch1, 15)
	require.NoError(t, err)

	// verify index is 15
	idx, err = bri.get(ch1)
	require.NoError(t, err)
	require.EqualValues(t, 15, idx)

	// set index to 14
	err = bri.update(ch1, 14)
	require.NoError(t, err)

	// verify index is still 15
	idx, err = bri.get(ch1)
	require.NoError(t, err)
	require.EqualValues(t, 15, idx)

	// remove index
	err = bri.delete(ch1)
	require.NoError(t, err)

	// verify index is back to 0
	idx, err = bri.get(ch1)
	require.NoError(t, err)
	require.EqualValues(t, 0, idx)
}
