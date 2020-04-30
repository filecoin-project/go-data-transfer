package registry

import (
	"sync"

	"github.com/filecoin-project/go-data-transfer/encoding"
	"golang.org/x/xerrors"
)

// Processor is an interface that processes a certain type of encodable objects
// in a registry. The actual specifics of the interface that must be satisfied are
// left to the user of the registry
type Processor interface{}

// Identifier is a unique string identifier for a type of encodable object in a
// registry
type Identifier string

// Entry is a type of object in a registry. It must be encodable and must
// have a single method that uniquely identifies its type
type Entry interface {
	encoding.Encodable
	// Type is a unique string identifier for this voucher type
	Type() Identifier
}

type registryEntry struct {
	decoder   encoding.Decoder
	processor Processor
}

// Registry maintans a register of types of encodable objects and a corresponding
// processor for those objects
// The encodable types must have a method Type() that specifies and identifier
// so they correct decoding function and processor can be identified based
// on this unique identifier
type Registry struct {
	registryLk sync.RWMutex
	entries    map[Identifier]registryEntry
}

// NewRegistry initialzes a new registy
func NewRegistry() *Registry {
	return &Registry{
		entries: make(map[Identifier]registryEntry),
	}
}

// Register registers the given processor for the given entry type
func (r *Registry) Register(entry Entry, processor Processor) error {
	identifier := entry.Type()
	decoder, err := encoding.NewDecoder(entry)
	if err != nil {
		return xerrors.Errorf("registering entry type %s: %w", identifier, err)
	}
	r.registryLk.Lock()
	defer r.registryLk.Unlock()
	if _, ok := r.entries[identifier]; ok {
		return xerrors.Errorf("identifier already registered: %s", identifier)
	}
	r.entries[identifier] = registryEntry{decoder, processor}
	return nil
}

// Decoder gets a decoder for the given identifier
func (r *Registry) Decoder(identifier Identifier) (encoding.Decoder, bool) {
	r.registryLk.RLock()
	entry, has := r.entries[identifier]
	r.registryLk.RUnlock()
	return entry.decoder, has
}

// Processor gets the processing interface for the given identifer
func (r *Registry) Processor(identifier Identifier) (Processor, bool) {
	r.registryLk.RLock()
	entry, has := r.entries[identifier]
	r.registryLk.RUnlock()
	return entry.processor, has
}
