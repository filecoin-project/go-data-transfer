package executor_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/ipfs/go-graphsync"
	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/testutil"
	"github.com/filecoin-project/go-data-transfer/v2/transport/graphsync/executor"
)

func TestExecutor(t *testing.T) {
	ctx := context.Background()
	chid := testutil.GenerateChannelID()
	testCases := map[string]struct {
		responseProgresses         []graphsync.ResponseProgress
		responseErrors             []error
		hasCompletedRequestHandler bool
		expectedEventRecord        fakeEvents
	}{
		"simple no errors, no listener": {
			expectedEventRecord: fakeEvents{
				completedChannel: chid,
				completedError:   nil,
			},
		},
		"simple with error, no listener": {
			responseErrors: []error{errors.New("something went wrong")},
			expectedEventRecord: fakeEvents{
				completedChannel: chid,
				completedError:   fmt.Errorf("channel %s: graphsync request failed to complete: %w", chid, errors.New("something went wrong")),
			},
		},
		"client cancelled request error, no listener": {
			responseErrors: []error{graphsync.RequestClientCancelledErr{}},
			expectedEventRecord: fakeEvents{
				cancelledChannel: chid,
				cancelledErr:     errors.New("graphsync request cancelled"),
			},
		},
		// no events called here
		"cancelled request error, no listener": {
			responseErrors: []error{graphsync.RequestCancelledErr{}},
		},
		"has completed request handler": {
			expectedEventRecord: fakeEvents{
				completedChannel: chid,
				completedError:   nil,
			},
			hasCompletedRequestHandler: true,
		},
	}
	for testCase, data := range testCases {
		t.Run(testCase, func(t *testing.T) {
			responseChan := make(chan graphsync.ResponseProgress)
			errChan := make(chan error)
			events := &fakeEvents{}
			fcrl := &fakeCompletedRequestListener{}

			completed := make(chan struct{})
			var onCompleteOnce sync.Once

			onComplete := func() {
				onCompleteOnce.Do(func() {
					close(completed)
				})
			}
			e := executor.NewExecutor(chid, responseChan, errChan, onComplete)
			if data.hasCompletedRequestHandler {
				e.Start(events, fcrl.complete)
			} else {
				e.Start(events, nil)
			}

			for _, progress := range data.responseProgresses {
				select {
				case <-ctx.Done():
					t.Fatal("unable to queue all responses")
				case responseChan <- progress:
				}
			}
			close(responseChan)

			for _, err := range data.responseErrors {
				select {
				case <-ctx.Done():
					t.Fatal("unable to queue all errors")
				case errChan <- err:
				}
			}
			close(errChan)

			select {
			case <-ctx.Done():
				t.Fatal("did not complete request")
			case <-completed:
			}

			require.Equal(t, data.expectedEventRecord, *events)
			if data.hasCompletedRequestHandler {
				require.Equal(t, chid, fcrl.calledChannel)
			} else {
				require.NotEqual(t, chid, fcrl.calledChannel)
			}
		})
	}
}

type fakeEvents struct {
	completedChannel datatransfer.ChannelID
	completedError   error
	cancelledChannel datatransfer.ChannelID
	cancelledErr     error
}

func (fe *fakeEvents) OnChannelCompleted(chid datatransfer.ChannelID, err error) error {
	fe.completedChannel = chid
	fe.completedError = err
	return nil
}

func (fe *fakeEvents) OnRequestCancelled(chid datatransfer.ChannelID, err error) error {
	fe.cancelledChannel = chid
	fe.cancelledErr = err
	return nil
}

type fakeCompletedRequestListener struct {
	calledChannel datatransfer.ChannelID
}

func (fcrl *fakeCompletedRequestListener) complete(channelID datatransfer.ChannelID) {
	fcrl.calledChannel = channelID
}
