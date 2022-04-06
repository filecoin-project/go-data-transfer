package channels

import (
	"sync"
	"sync/atomic"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
)

type readIndexFn func(datatransfer.ChannelID) (int64, error)

type cacheKey struct {
	evt  datatransfer.EventCode
	chid datatransfer.ChannelID
}

type blockIndexCache struct {
	lk     sync.RWMutex
	values map[cacheKey]*int64
}

func newBlockIndexCache() *blockIndexCache {
	return &blockIndexCache{
		values: make(map[cacheKey]*int64),
	}
}

func (bic *blockIndexCache) getValue(evt datatransfer.EventCode, chid datatransfer.ChannelID, readFromOriginal readIndexFn) (*int64, error) {
	idxKey := cacheKey{evt, chid}
	bic.lk.RLock()
	value := bic.values[idxKey]
	bic.lk.RUnlock()
	if value != nil {
		return value, nil
	}
	bic.lk.Lock()
	defer bic.lk.Unlock()
	value = bic.values[idxKey]
	if value != nil {
		return value, nil
	}
	newValue, err := readFromOriginal(chid)
	if err != nil {
		return nil, err
	}
	bic.values[idxKey] = &newValue
	return &newValue, nil
}

func (bic *blockIndexCache) updateIfGreater(evt datatransfer.EventCode, chid datatransfer.ChannelID, newIndex int64, readFromOriginal readIndexFn) (bool, error) {
	value, err := bic.getValue(evt, chid, readFromOriginal)
	if err != nil {
		return false, err
	}
	for {
		currentIndex := atomic.LoadInt64(value)
		if newIndex <= currentIndex {
			return false, nil
		}
		if atomic.CompareAndSwapInt64(value, currentIndex, newIndex) {
			return true, nil
		}
	}
}

type progressState struct {
	dataLimit uint64
	progress  *uint64
}

type readProgressFn func(datatransfer.ChannelID) (dataLimit uint64, progress uint64, err error)

type progressCache struct {
	lk     sync.RWMutex
	values map[datatransfer.ChannelID]progressState
}

func newProgressCache() *progressCache {
	return &progressCache{
		values: make(map[datatransfer.ChannelID]progressState),
	}
}

func (pc *progressCache) getValue(chid datatransfer.ChannelID, readProgress readProgressFn) (progressState, error) {
	pc.lk.RLock()
	value, ok := pc.values[chid]
	pc.lk.RUnlock()
	if ok {
		return value, nil
	}
	pc.lk.Lock()
	defer pc.lk.Unlock()
	value, ok = pc.values[chid]
	if ok {
		return value, nil
	}
	dataLimit, progress, err := readProgress(chid)
	if err != nil {
		return progressState{}, err
	}
	newValue := progressState{
		dataLimit: dataLimit,
		progress:  &progress,
	}
	pc.values[chid] = newValue
	return newValue, nil
}

func (pc *progressCache) progress(chid datatransfer.ChannelID, additionalData uint64, readFromOriginal readProgressFn) (bool, error) {
	state, err := pc.getValue(chid, readFromOriginal)
	if err != nil {
		return false, err
	}
	total := atomic.AddUint64(state.progress, additionalData)
	return state.dataLimit != 0 && total >= state.dataLimit, nil
}

func (pc *progressCache) setDataLimit(chid datatransfer.ChannelID, newLimit uint64) {
	pc.lk.RLock()
	value, ok := pc.values[chid]
	pc.lk.RUnlock()
	if !ok {
		return
	}
	pc.lk.Lock()
	defer pc.lk.Unlock()
	value, ok = pc.values[chid]
	if !ok {
		return
	}
	value.dataLimit = newLimit
	pc.values[chid] = value
}
