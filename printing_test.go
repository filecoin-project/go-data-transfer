package datatransfer_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
)

func TestPrinting(t *testing.T) {
	for key, value := range datatransfer.Events {
		require.Equal(t, fmt.Sprintf("%v", key), value)
	}
	for key, value := range datatransfer.Statuses {
		require.Equal(t, fmt.Sprintf("%v", key), value)
	}
}
