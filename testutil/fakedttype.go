package testutil

import "github.com/filecoin-project/go-data-transfer/registry"

//go:generate cbor-gen-for FakeDTType

// FakeDTType simple fake type for using with registries
type FakeDTType struct {
	Data string
}

// Type satisfies registry.Entry
func (ft FakeDTType) Type() registry.Identifier {
	return "FakeDTType"
}

var _ registry.Entry = &FakeDTType{}
