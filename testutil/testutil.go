package testutil

import (
	"context"
	"testing"

	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime/datamodel"
	basicnode "github.com/ipld/go-ipld-prime/node/basic"
	"github.com/ipld/go-ipld-prime/traversal/selector"
	"github.com/ipld/go-ipld-prime/traversal/selector/builder"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
)

// var prioritySeq int
var seedSeq int64

var peerSeq int

// ContainsPeer returns true if a peer is found n a list of peers.
func ContainsPeer(peers []peer.ID, p peer.ID) bool {
	for _, n := range peers {
		if p == n {
			return true
		}
	}
	return false
}

// IndexOf returns the index of a given cid in an array of blocks
func IndexOf(blks []blocks.Block, c cid.Cid) int {
	for i, n := range blks {
		if n.Cid() == c {
			return i
		}
	}
	return -1
}

// ContainsBlock returns true if a block is found n a list of blocks
func ContainsBlock(blks []blocks.Block, block blocks.Block) bool {
	return IndexOf(blks, block.Cid()) != -1
}

// AssertEqualSelector asserts two requests have the same valid selector
func AssertEqualSelector(t *testing.T, expectedRequest datatransfer.Request, request datatransfer.Request) {
	expectedSelector, err := expectedRequest.Selector()
	require.NoError(t, err)
	selector, err := request.Selector()
	require.NoError(t, err)
	require.Equal(t, expectedSelector, selector)
}

// AllSelector just returns a new instance of a "whole dag selector"
func AllSelector() datamodel.Node {
	ssb := builder.NewSelectorSpecBuilder(basicnode.Prototype.Any)
	return ssb.ExploreRecursive(selector.RecursionLimitNone(),
		ssb.ExploreAll(ssb.ExploreRecursiveEdge())).Node()
}

// StartAndWaitForReady is a utility function to start a module and verify it reaches the ready state
func StartAndWaitForReady(ctx context.Context, t *testing.T, manager datatransfer.Manager) {
	ready := make(chan error, 1)
	manager.OnReady(func(err error) {
		ready <- err
	})
	require.NoError(t, manager.Start(ctx))
	select {
	case <-ctx.Done():
		t.Fatal("did not finish starting up module")
	case err := <-ready:
		require.NoError(t, err)
	}
}
