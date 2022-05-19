package actionrecorders_test

import (
	"testing"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/testutil"
	"github.com/filecoin-project/go-data-transfer/v2/transport/helpers/actionrecorders"
	"github.com/stretchr/testify/require"
)

func TestChannelActionsRecorder(t *testing.T) {
	msg := testutil.NewDTRequest(t, 0)
	testCases := map[string]struct {
		actions        datatransfer.ChannelActionFunc
		expectedResult actionrecorders.ChannelActionsRecorder
	}{
		"do nothing": {
			actions:        func(datatransfer.ChannelActions) {},
			expectedResult: actionrecorders.ChannelActionsRecorder{},
		},
		"close channel": {
			actions: func(actions datatransfer.ChannelActions) {
				actions.CloseChannel()
			},
			expectedResult: actionrecorders.ChannelActionsRecorder{
				DoClose: true,
			},
		},
		"send message": {
			actions: func(actions datatransfer.ChannelActions) {
				actions.SendMessage(msg)
			},
			expectedResult: actionrecorders.ChannelActionsRecorder{
				MessageSent: msg,
			},
		},
		"do both": {
			actions: func(actions datatransfer.ChannelActions) {
				actions.SendMessage(msg)
				actions.CloseChannel()
			},
			expectedResult: actionrecorders.ChannelActionsRecorder{
				MessageSent: msg,
				DoClose:     true,
			},
		},
	}
	for testCase, data := range testCases {
		t.Run(testCase, func(t *testing.T) {
			ca := actionrecorders.ChannelActionsRecorder{}
			data.actions(&ca)
			require.Equal(t, data.expectedResult, ca)
		})
	}
}
