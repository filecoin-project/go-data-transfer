package channels

import (
	"encoding/binary"
	"sync"

	"github.com/ipfs/go-datastore"
	"golang.org/x/xerrors"

	datatransfer "github.com/filecoin-project/go-data-transfer"
)

// blocksReceivedIndex keeps track of how many blocks have been transferred
// over each channel. The index is the position in the ordered stream of
// blocks. When a data transfer is restarted, the data receiver includes
// this index in the restart request so that the sender doesn't need to
// resend all previous blocks.
type blocksReceivedIndex struct {
	lk sync.Mutex
	ds datastore.Datastore
}

func newBlocksReceivedIndex(ds datastore.Datastore) *blocksReceivedIndex {
	return &blocksReceivedIndex{ds: ds}
}

// update sets the value of the index, if it's greater than the existing
// value
func (b *blocksReceivedIndex) update(chid datatransfer.ChannelID, newIdx int64) error {
	b.lk.Lock()
	defer b.lk.Unlock()

	// Get the current value
	idx, err := b.unlockedGet(chid)
	if err != nil {
		return err
	}

	// Check if the new value is higher
	if newIdx <= idx {
		return nil
	}

	// Set the new value
	idxBz := make([]byte, 8)
	binary.LittleEndian.PutUint64(idxBz, (uint64)(newIdx))
	return b.ds.Put(datastore.NewKey(chid.String()), idxBz)
}

// get the value of the index
// returns zero if the index hasn't been set
func (b *blocksReceivedIndex) get(chid datatransfer.ChannelID) (int64, error) {
	b.lk.Lock()
	defer b.lk.Unlock()

	return b.unlockedGet(chid)
}

func (b *blocksReceivedIndex) unlockedGet(chid datatransfer.ChannelID) (int64, error) {
	totalBz, err := b.ds.Get(datastore.NewKey(chid.String()))
	if err != nil {
		// If the index hasn't been set yet, just return 0
		if xerrors.Is(datastore.ErrNotFound, err) {
			return 0, nil
		}
		return 0, err
	}

	total := binary.LittleEndian.Uint64(totalBz)
	return int64(total), nil
}

// delete cleans up the index
func (b *blocksReceivedIndex) delete(chid datatransfer.ChannelID) error {
	return b.ds.Delete(datastore.NewKey(chid.String()))
}
