package registry

import (
	"fmt"
	"sync"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
)

// Processor is an interface that processes a certain type of encodable objects
// in a registry. The actual specifics of the interface that must be satisfied are
// left to the user of the registry
type Processor interface{}

// Registry maintans a register of types of encodable objects and a corresponding
// processor for those objects
// The encodable types must have a method Type() that specifies and identifier
// so they correct decoding function and processor can be identified based
// on this unique identifier
type Registry struct {
	registryLk sync.RWMutex
	entries    map[datatransfer.TypeIdentifier]Processor
}

// NewRegistry initialzes a new registy
func NewRegistry() *Registry {
	return &Registry{
		entries: make(map[datatransfer.TypeIdentifier]Processor),
	}
}

// Register registers the given processor for the given entry type
func (r *Registry) Register(identifier datatransfer.TypeIdentifier, processor Processor) error {
	r.registryLk.Lock()
	defer r.registryLk.Unlock()
	if _, ok := r.entries[identifier]; ok {
		return fmt.Errorf("identifier already registered: %s", identifier)
	}
	r.entries[identifier] = processor
	return nil
}

// Processor gets the processing interface for the given identifer
func (r *Registry) Processor(identifier datatransfer.TypeIdentifier) (Processor, bool) {
	r.registryLk.RLock()
	entry, has := r.entries[identifier]
	r.registryLk.RUnlock()
	return entry, has
}

// Each iterates through all of the entries in this registry
func (r *Registry) Each(process func(datatransfer.TypeIdentifier, Processor) error) error {
	r.registryLk.RLock()
	defer r.registryLk.RUnlock()
	for identifier, processor := range r.entries {
		err := process(identifier, processor)
		if err != nil {
			return err
		}
	}
	return nil
}
