package registry_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/registry"
)

func TestRegistry(t *testing.T) {
	r := registry.NewRegistry()
	t.Run("it registers", func(t *testing.T) {
		err := r.Register(datatransfer.TypeIdentifier("FakeDTType"), func() {})
		require.NoError(t, err)
	})
	t.Run("it errors when registred again", func(t *testing.T) {
		err := r.Register(datatransfer.TypeIdentifier("FakeDTType"), func() {})
		require.EqualError(t, err, "identifier already registered: FakeDTType")
	})
	t.Run("it reads processors", func(t *testing.T) {
		processor, has := r.Processor("FakeDTType")
		require.True(t, has)
		require.NotNil(t, processor)
		processor, has = r.Processor("OtherType")
		require.False(t, has)
		require.Nil(t, processor)
	})

}
