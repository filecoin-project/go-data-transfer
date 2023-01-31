package transportoptions

import (
	"fmt"
	"sync"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
)

type TransportOptions struct {
	optionsLk sync.RWMutex
	options   map[datatransfer.ChannelID][]datatransfer.TransportOption
}

func NewTransportOptions() *TransportOptions {
	return &TransportOptions{
		options: make(map[datatransfer.ChannelID][]datatransfer.TransportOption),
	}
}

func (to *TransportOptions) SetOptions(chid datatransfer.ChannelID, options []datatransfer.TransportOption) {
	to.optionsLk.Lock()
	to.options[chid] = options
	to.optionsLk.Unlock()
}

func (to *TransportOptions) ApplyOptions(chid datatransfer.ChannelID, transport datatransfer.Transport) error {
	to.optionsLk.RLock()
	defer to.optionsLk.RUnlock()

	for _, transportOption := range to.options[chid] {
		err := transportOption(chid, transport)
		if err != nil {
			return fmt.Errorf("applying transport option: %w", err)
		}
	}
	return nil
}

func (to *TransportOptions) ClearOptions(chid datatransfer.ChannelID) {
	to.optionsLk.Lock()
	defer to.optionsLk.Unlock()
	delete(to.options, chid)

}

func (to *TransportOptions) ClearAll() {
	to.optionsLk.Lock()
	defer to.optionsLk.Unlock()
	// reset in case someone continues to use the span index
	to.options = make(map[datatransfer.ChannelID][]datatransfer.TransportOption)
}
