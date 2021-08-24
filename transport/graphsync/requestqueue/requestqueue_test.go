package requestqueue_test

import (
	"context"
	"sync/atomic"
	"testing"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/testutil"
	"github.com/filecoin-project/go-data-transfer/transport/graphsync/requestqueue"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-graphsync"
	ipld "github.com/ipld/go-ipld-prime"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	peer "github.com/libp2p/go-libp2p-core/peer"
	"github.com/stretchr/testify/require"
)

func TestRequestQueue(t *testing.T) {
	ctx := context.Background()
	peers := testutil.GeneratePeers(3)
	transferIds := testutil.GenerateTransferIDs(4)
	cids := testutil.GenerateCids(4)
	stor := testutil.AllSelector()
	executedChannels := make(chan datatransfer.ChannelID)
	advance := make(chan struct{})
	inProgressRequests := uint64(0)
	fakeDeferredRequestFn := func(ctx context.Context, chid datatransfer.ChannelID, dataSender peer.ID, root ipld.Link, stor ipld.Node, doNotSendCids []cid.Cid, exts []graphsync.ExtensionData) {
		atomic.AddUint64(&inProgressRequests, 1)
		executedChannels <- chid
		<-advance
		atomic.AddUint64(&inProgressRequests, ^uint64(0))
	}

	requestQueue := requestqueue.NewRequestQueue(fakeDeferredRequestFn, 2)
	// queue requests, putting 3 for first peer in first, followed by one for second peer
	for i := 0; i < 3; i++ {
		requestQueue.AddRequest(ctx, datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: transferIds[i]}, peers[1], cidlink.Link{Cid: cids[i]}, stor, nil, nil)
	}
	requestQueue.AddRequest(ctx, datatransfer.ChannelID{Initiator: peers[0], Responder: peers[2], ID: transferIds[3]}, peers[2], cidlink.Link{Cid: cids[3]}, stor, nil, nil)

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

	// read next
	advance <- struct{}{}
	fourthChannelID := <-executedChannels
	require.Equal(t, atomic.LoadUint64(&inProgressRequests), uint64(2))
	require.Equal(t, datatransfer.ChannelID{Initiator: peers[0], Responder: peers[1], ID: transferIds[2]}, fourthChannelID)
}
