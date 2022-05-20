package registry_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/filecoin-project/go-data-transfer/v2/registry"
	"github.com/filecoin-project/go-data-transfer/v2/testutil"
)

func TestRegistry(t *testing.T) {
	r := registry.NewRegistry()
	t.Run("it registers", func(t *testing.T) {
		err := r.Register(testutil.TestVoucherType, func() {})
		require.NoError(t, err)
	})
	t.Run("it errors when registred again", func(t *testing.T) {
		err := r.Register(testutil.TestVoucherType, func() {})
		require.EqualError(t, err, "identifier already registered: TestVoucher")
	})
	t.Run("it reads processors", func(t *testing.T) {
		processor, has := r.Processor("TestVoucher")
		require.True(t, has)
		require.NotNil(t, processor)
		processor, has = r.Processor("OtherType")
		require.False(t, has)
		require.Nil(t, processor)
	})

}
