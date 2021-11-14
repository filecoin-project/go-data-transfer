package bitswap

import (
	"bytes"
	"context"
	"encoding/binary"
	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/impl"
	"github.com/filecoin-project/go-data-transfer/network"
	"github.com/filecoin-project/go-data-transfer/testutil"
	"github.com/ipfs/go-bitswap/authbs"
	bsnet "github.com/ipfs/go-bitswap/network"
	bsserver "github.com/ipfs/go-bitswap/server"
	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-blockservice"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	leveldb "github.com/ipfs/go-ds-leveldb"
	blockstore "github.com/ipfs/go-ipfs-blockstore"
	chunker "github.com/ipfs/go-ipfs-chunker"
	offline "github.com/ipfs/go-ipfs-exchange-offline"
	mockrouting "github.com/ipfs/go-ipfs-routing/mock"
	offline2 "github.com/ipfs/go-ipfs-routing/offline"
	ipldformat "github.com/ipfs/go-ipld-format"
	"github.com/ipfs/go-merkledag"
	"github.com/ipfs/go-unixfs/importer/balanced"
	ihelper "github.com/ipfs/go-unixfs/importer/helpers"
	"github.com/ipld/go-ipld-prime"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/peer"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"
	"math/rand"
	"os"
	"sync"
	"testing"
	"time"
)

func TestName(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := mocknet.New(ctx)
	h1, err := m.GenPeer()
	if err != nil {
		t.Fatal(err)
	}

	h2, err := m.GenPeer()
	if err != nil {
		t.Fatal(err)
	}

	if err := m.LinkAll(); err != nil {
		t.Fatal(err)
	}

	ds1, err := leveldb.NewDatastore("", nil)
	if err != nil {
		t.Fatal(err)
	}

	ds2, err := leveldb.NewDatastore("", nil)
	if err != nil {
		t.Fatal(err)
	}

	bs1 := blockstore.NewBlockstore(ds1)
	rtlnk, _ := LoadRandomData(ctx, t, bs1, 1024)
	root := rtlnk.(cidlink.Link)

	var chanid datatransfer.ChannelID
	var chanidLk sync.Mutex

	authPhrase := "open sesame"

	var h1ReceivedVoucherToken []byte
	bs1 = &authBS{
		Blockstore:   bs1,
		isAuthorized: func(p peer.ID, bsToken []byte) bool {
			transferID, err := binary.ReadUvarint(bytes.NewReader(bsToken))
			if err != nil {
				return false
			}
			return p == chanid.Initiator && uint64(chanid.ID) == transferID && bytes.Equal([]byte(authPhrase), h1ReceivedVoucherToken)
		},
	}
	s1 := bsserver.New(ctx,
		bsnet.NewFromIpfsHost(h1, offline2.NewOfflineRouter(datastore.NewMapDatastore(), mockrouting.MockValidator{} )),
		bs1,
	)
	_ = s1

	dt1 := createDt(t, h1, ds1)
	dt2 := createDt(t, h2, ds2)

	// dtRes receives either an error (failure) or nil (success) which is waited
	// on and handled below before exiting the function
	dtRes := make(chan error, 1)

	finish := func(err error) {
		select {
		case dtRes <- err:
		default:
		}
	}

	unsubscribe := dt1.SubscribeToEvents(func(event datatransfer.Event, state datatransfer.ChannelState) {
		// Copy chanid so it can be used later in the callback
		chanidLk.Lock()
		chanidCopy := chanid
		chanidLk.Unlock()

		// Skip all events that aren't related to this channel
		if state.ChannelID() != chanidCopy {
			return
		}

		switch event.Code {
		case datatransfer.NewVoucherResult:
			switch resType := state.LastVoucherResult().(type) {
			case *testutil.FakeDTType:
				h1ReceivedVoucherToken = []byte(resType.Data)
				t.Logf("received voucher %v", resType.Data)
			default:
				t.Logf("in-progress voucher result type: %v", resType)
			}
		case datatransfer.DataReceived:
		case datatransfer.DataReceivedProgress:
			t.Logf("progress %v", state.Received())
		case datatransfer.Complete:
			finish(nil)
		default:
			t.Logf("in-progress event code: %v", event.Code)
		}
	})
	defer unsubscribe()

	// Submit the retrieval deal proposal to the miner
	v2 := &testutil.FakeDTType{Data: authPhrase}
	newchid, err := dt2.OpenPullDataChannel(ctx, h1.ID(), v2, root.Cid, testutil.AllSelector())
	if err != nil {
		t.Fatal(err)
	}

	chanidLk.Lock()
	chanid = newchid
	chanidLk.Unlock()

	//if err := dt2.SendVoucher(ctx, newchid, v2); err != nil {
	//	t.Fatal(err)
	//}

	// Wait for the retrieval to finish before exiting the function
	select {
	case err := <-dtRes:
		if err != nil {
			t.Fatalf("data transfer failed: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(err)
	}
}

func createDt(t *testing.T, h host.Host, ds datastore.Batching) datatransfer.Manager {
	tmpcidlistdir, err := os.MkdirTemp("", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpcidlistdir)

	bn := bsnet.NewFromIpfsHost(h, offline2.NewOfflineRouter(datastore.NewMapDatastore(), mockrouting.MockValidator{} ))
	tp, err := New(bn)
	if err != nil {
		t.Fatal(err)
	}
	n := network.NewFromLibp2pHost(h)
	ds, err = leveldb.NewDatastore("", nil)
	if err != nil {
		t.Fatal(err)
	}
	dt, err := impl.NewDataTransfer(ds, tmpcidlistdir, n, tp)
	if err != nil {
		t.Fatal(err)
	}

	sv := testutil.NewStubbedValidator()
	require.NoError(t, dt.RegisterVoucherType(&testutil.FakeDTType{}, sv))
	require.NoError(t, dt.RegisterRevalidator(&testutil.FakeDTType{}, testutil.NewStubbedRevalidator()))
	require.NoError(t, dt.Start(context.Background()))
	return dt
}

type authBS struct {
	blockstore.Blockstore
	isAuthorized func(p peer.ID, bsToken []byte) bool
}

func (a *authBS) HasWithToken(c cid.Cid, p peer.ID, bsToken []byte) (bool, error) {
	if a.isAuthorized(p, bsToken) {
		return a.Has(c)
	}
	return false, nil
}

func (a *authBS) GetWithToken(c cid.Cid, p peer.ID, bsToken []byte) (blocks.Block, error) {
	if a.isAuthorized(p, bsToken) {
		return a.Get(c)
	}
	return nil, authbs.ErrAccessDenied
}

func (a *authBS) GetSizeWithToken(c cid.Cid, p peer.ID, bsToken []byte) (int, error) {
	if a.isAuthorized(p, bsToken) {
		return a.GetSize(c)
	}
	return 0, authbs.ErrAccessDenied
}

var _ authbs.AuthenticatedBlockstoreGets = (*authBS)(nil)

func LoadRandomData(ctx context.Context, t *testing.T, bstore blockstore.Blockstore, size int) (ipld.Link, []byte) {
	dsrv := merkledag.NewDAGService(blockservice.New(bstore, offline.Exchange(bstore)))

	data := make([]byte, size)
	rand.New(rand.NewSource(time.Now().UnixNano())).Read(data)

	// import to UnixFS
	bufferedDS := ipldformat.NewBufferedDAG(ctx, dsrv)

	params := ihelper.DagBuilderParams{
		Maxlinks:   1024,
		RawLeaves:  true,
		CidBuilder: nil,
		Dagserv:    bufferedDS,
	}

	db, err := params.New(chunker.NewSizeSplitter(bytes.NewReader(data), 128000))
	require.NoError(t, err)

	nd, err := balanced.Layout(db)
	require.NoError(t, err)

	err = bufferedDS.Commit()
	require.NoError(t, err)

	// save the original files bytes
	return cidlink.Link{Cid: nd.Cid()}, data
}

