package requestqueue_test

import (
	"context"
	"math"
	"sync/atomic"
	"testing"

	peer "github.com/libp2p/go-libp2p-core/peer"
	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/testutil"
	"github.com/filecoin-project/go-data-transfer/transport/graphsync/requestqueue"
)

func TestRequestQueue(t *testing.T) {
	ctx := context.Background()
	peers := testutil.GeneratePeers(3)
	transferIds := testutil.GenerateTransferIDs(4)
	executedChannels := make(chan datatransfer.ChannelID)
	advance := make(chan struct{})
	inProgressRequests := uint64(0)

	makeFakeRequest := func(p peer.ID, chid datatransfer.ChannelID) fakeRequest {
		return fakeRequest{chid, p, executedChannels, advance, &inProgressRequests}
	}

	requestQueue := requestqueue.NewRequestQueue(2)
	// queue requests, putting 3 for first peer in first, followed by one for second peer
	for i := 0; i < 3; i++ {
		requestQueue.AddRequest(ctx, makeFakeRequest(peers[1], datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: transferIds[i]}), math.MaxInt32)
	}
	requestQueue.AddRequest(ctx, makeFakeRequest(peers[2], datatransfer.ChannelID{Initiator: peers[0], Responder: peers[2], ID: transferIds[3]}), math.MaxInt32)

	requestQueue.Start(ctx)

	// read the first two requests
	firstChannelID := <-executedChannels
	secondChannelID := <-executedChannels

	// verify exactly two requests are executed, no more
	require.Equal(t, atomic.LoadUint64(&inProgressRequests), uint64(2))

	// the first two requests should be split up evenly among peers -- the first queued up by each (not possible to predict order
	// within that due concurrency)
	if firstChannelID.Responder == peers[1] {
		require.Equal(t, datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: transferIds[0]}, firstChannelID)
		require.Equal(t, datatransfer.ChannelID{Initiator: peers[0], Responder: peers[2], ID: transferIds[3]}, secondChannelID)
	} else {
		require.Equal(t, datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: transferIds[0]}, secondChannelID)
		require.Equal(t, datatransfer.ChannelID{Initiator: peers[0], Responder: peers[2], ID: transferIds[3]}, firstChannelID)
	}

	// read next
	advance <- struct{}{}
	thirdChannelId := <-executedChannels
	require.Equal(t, atomic.LoadUint64(&inProgressRequests), uint64(2))
	require.Equal(t, datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: transferIds[1]}, thirdChannelId)

	// test adding a new task with a channelID that is pending
	requestQueue.AddRequest(ctx, makeFakeRequest(peers[1], datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: transferIds[2]}), math.MaxInt32)

	// test adding a new task with a channelID that is active
	requestQueue.AddRequest(ctx, makeFakeRequest(peers[1], datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: transferIds[1]}), math.MaxInt32)

	// read next
	advance <- struct{}{}
	fourthChannelID := <-executedChannels
	require.Equal(t, atomic.LoadUint64(&inProgressRequests), uint64(2))
	require.Equal(t, datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: transferIds[2]}, fourthChannelID)

	// read next
	advance <- struct{}{}
	fifthChannelID := <-executedChannels
	require.Equal(t, atomic.LoadUint64(&inProgressRequests), uint64(2))
	// the next task should be the one that was added while a duplicate was active -- it will run again.
	// the one that was added that duplicated a pending task is ignored
	require.Equal(t, datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: transferIds[1]}, fifthChannelID)

}

type fakeRequest struct {
	channelID          datatransfer.ChannelID
	dataSender         peer.ID
	executedChannels   chan<- datatransfer.ChannelID
	advance            <-chan struct{}
	inProgressRequests *uint64
}

func (fr fakeRequest) DataSender() peer.ID {
	return fr.dataSender
}
func (fr fakeRequest) ChannelID() datatransfer.ChannelID {
	return fr.channelID
}
func (fr fakeRequest) Execute() {
	atomic.AddUint64(fr.inProgressRequests, 1)
	fr.executedChannels <- fr.channelID
	<-fr.advance
	atomic.AddUint64(fr.inProgressRequests, ^uint64(0))
}
