package impl_test

import (
	"bytes"
	"context"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/ipfs/go-blockservice"
	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/namespace"
	dss "github.com/ipfs/go-datastore/sync"
	"github.com/ipfs/go-graphsync"
	gsimpl "github.com/ipfs/go-graphsync/impl"
	gsmsg "github.com/ipfs/go-graphsync/message"
	gsnet "github.com/ipfs/go-graphsync/network"
	"github.com/ipfs/go-graphsync/storeutil"
	bstore "github.com/ipfs/go-ipfs-blockstore"
	offline "github.com/ipfs/go-ipfs-exchange-offline"
	ipldformat "github.com/ipfs/go-ipld-format"
	"github.com/ipfs/go-merkledag"
	"github.com/ipld/go-ipld-prime"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-core/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/filecoin-project/go-storedcounter"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/encoding"
	. "github.com/filecoin-project/go-data-transfer/impl"
	"github.com/filecoin-project/go-data-transfer/message"
	"github.com/filecoin-project/go-data-transfer/network"
	"github.com/filecoin-project/go-data-transfer/testutil"
	tp "github.com/filecoin-project/go-data-transfer/transport/graphsync"
	"github.com/filecoin-project/go-data-transfer/transport/graphsync/extension"
)

const loremFile = "lorem.txt"

// nil means use the default protocols
// tests data transfer for the following protocol combinations:
// default protocol -> default protocols
// old protocol -> default protocols
// default protocols -> old protocol
var protocolsForTest = map[string]struct {
	host1Protocols []protocol.ID
	host2Protocols []protocol.ID
}{
	"(new -> new)":      {nil, nil},
	"(old -> new, old)": {[]protocol.ID{datatransfer.ProtocolDataTransfer1_0}, nil},
	"(new, old -> old)": {nil, []protocol.ID{datatransfer.ProtocolDataTransfer1_0}},
}

func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	testCases := map[string]struct {
		isPull            bool
		customSourceStore bool
		customTargetStore bool
	}{
		"roundtrip for push requests": {},
		"roundtrip for pull requests": {
			isPull: true,
		},
		"custom source, push": {
			customSourceStore: true,
		},
		"custom source, pull": {
			isPull:            true,
			customSourceStore: true,
		},
		"custom dest, push": {
			customTargetStore: true,
		},
		"custom dest, pull": {
			isPull:            true,
			customTargetStore: true,
		},
		"custom both sides, push": {
			customSourceStore: true,
			customTargetStore: true,
		},
		"custom both sides, pull": {
			isPull:            true,
			customSourceStore: true,
			customTargetStore: true,
		},
	}
	for testCase, data := range testCases {
		for pname, ps := range protocolsForTest {
			t.Run(testCase+pname, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()

				gsData := testutil.NewGraphsyncTestingData(ctx, t, ps.host1Protocols, ps.host2Protocols)
				host1 := gsData.Host1 // initiator, data sender
				host2 := gsData.Host2 // data recipient

				tp1 := gsData.SetupGSTransportHost1()
				tp2 := gsData.SetupGSTransportHost2()

				dt1, err := NewDataTransfer(gsData.DtDs1, gsData.TempDir1, gsData.DtNet1, tp1, gsData.StoredCounter1)
				require.NoError(t, err)
				testutil.StartAndWaitForReady(ctx, t, dt1)
				dt2, err := NewDataTransfer(gsData.DtDs2, gsData.TempDir2, gsData.DtNet2, tp2, gsData.StoredCounter2)
				require.NoError(t, err)
				testutil.StartAndWaitForReady(ctx, t, dt2)

				finished := make(chan struct{}, 2)
				errChan := make(chan struct{}, 2)
				opened := make(chan struct{}, 2)
				sent := make(chan uint64, 21)
				received := make(chan uint64, 21)
				var subscriber datatransfer.Subscriber = func(event datatransfer.Event, channelState datatransfer.ChannelState) {
					if event.Code == datatransfer.DataQueued {
						if channelState.Queued() > 0 {
							sent <- channelState.Queued()
						}
					}

					if event.Code == datatransfer.DataReceived {
						if channelState.Received() > 0 {
							received <- channelState.Received()
						}
					}

					if channelState.Status() == datatransfer.Completed {
						finished <- struct{}{}
					}
					if event.Code == datatransfer.Error {
						errChan <- struct{}{}
					}
					if event.Code == datatransfer.Open {
						opened <- struct{}{}
					}
				}
				dt1.SubscribeToEvents(subscriber)
				dt2.SubscribeToEvents(subscriber)
				voucher := testutil.FakeDTType{Data: "applesauce"}
				sv := testutil.NewStubbedValidator()

				var sourceDagService ipldformat.DAGService
				if data.customSourceStore {
					ds := dss.MutexWrap(datastore.NewMapDatastore())
					bs := bstore.NewBlockstore(namespace.Wrap(ds, datastore.NewKey("blockstore")))
					loader := storeutil.LoaderForBlockstore(bs)
					storer := storeutil.StorerForBlockstore(bs)
					sourceDagService = merkledag.NewDAGService(blockservice.New(bs, offline.Exchange(bs)))
					err := dt1.RegisterTransportConfigurer(&testutil.FakeDTType{}, func(channelID datatransfer.ChannelID, testVoucher datatransfer.Voucher, transport datatransfer.Transport) {
						fv, ok := testVoucher.(*testutil.FakeDTType)
						if ok && fv.Data == voucher.Data {
							gsTransport, ok := transport.(*tp.Transport)
							if ok {
								err := gsTransport.UseStore(channelID, loader, storer)
								require.NoError(t, err)
							}
						}
					})
					require.NoError(t, err)
				} else {
					sourceDagService = gsData.DagService1
				}
				root, origBytes := testutil.LoadUnixFSFile(ctx, t, sourceDagService, loremFile)
				rootCid := root.(cidlink.Link).Cid

				var destDagService ipldformat.DAGService
				if data.customTargetStore {
					ds := dss.MutexWrap(datastore.NewMapDatastore())
					bs := bstore.NewBlockstore(namespace.Wrap(ds, datastore.NewKey("blockstore")))
					loader := storeutil.LoaderForBlockstore(bs)
					storer := storeutil.StorerForBlockstore(bs)
					destDagService = merkledag.NewDAGService(blockservice.New(bs, offline.Exchange(bs)))
					err := dt2.RegisterTransportConfigurer(&testutil.FakeDTType{}, func(channelID datatransfer.ChannelID, testVoucher datatransfer.Voucher, transport datatransfer.Transport) {
						fv, ok := testVoucher.(*testutil.FakeDTType)
						if ok && fv.Data == voucher.Data {
							gsTransport, ok := transport.(*tp.Transport)
							if ok {
								err := gsTransport.UseStore(channelID, loader, storer)
								require.NoError(t, err)
							}
						}
					})
					require.NoError(t, err)
				} else {
					destDagService = gsData.DagService2
				}

				var chid datatransfer.ChannelID
				if data.isPull {
					sv.ExpectSuccessPull()
					require.NoError(t, dt1.RegisterVoucherType(&testutil.FakeDTType{}, sv))
					chid, err = dt2.OpenPullDataChannel(ctx, host1.ID(), &voucher, rootCid, gsData.AllSelector)
				} else {
					sv.ExpectSuccessPush()
					require.NoError(t, dt2.RegisterVoucherType(&testutil.FakeDTType{}, sv))
					chid, err = dt1.OpenPushDataChannel(ctx, host2.ID(), &voucher, rootCid, gsData.AllSelector)
				}
				require.NoError(t, err)
				opens := 0
				completes := 0
				sentIncrements := make([]uint64, 0, 21)
				receivedIncrements := make([]uint64, 0, 21)
				for opens < 2 || completes < 2 || len(sentIncrements) < 21 || len(receivedIncrements) < 21 {
					select {
					case <-ctx.Done():
						t.Fatal("Did not complete successful data transfer")
					case <-finished:
						completes++
					case <-opened:
						opens++
					case sentIncrement := <-sent:
						sentIncrements = append(sentIncrements, sentIncrement)
					case receivedIncrement := <-received:
						receivedIncrements = append(receivedIncrements, receivedIncrement)
					case <-errChan:
						t.Fatal("received error on data transfer")
					}
				}
				require.Equal(t, sentIncrements, receivedIncrements)
				testutil.VerifyHasFile(ctx, t, destDagService, root, origBytes)
				if data.isPull {
					assert.Equal(t, chid.Initiator, host2.ID())
				} else {
					assert.Equal(t, chid.Initiator, host1.ID())
				}
			})
		}
	} //
}

func TestMultipleRoundTripMultipleStores(t *testing.T) {
	ctx := context.Background()
	testCases := map[string]struct {
		isPull       bool
		requestCount int
	}{
		"multiple roundtrip for push requests": {
			requestCount: 2,
		},
		"multiple roundtrip for pull requests": {
			isPull:       true,
			requestCount: 2,
		},
	}
	for testCase, data := range testCases {
		t.Run(testCase, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			gsData := testutil.NewGraphsyncTestingData(ctx, t, nil, nil)
			host1 := gsData.Host1 // initiator, data sender
			host2 := gsData.Host2 // data recipient

			tp1 := gsData.SetupGSTransportHost1()
			tp2 := gsData.SetupGSTransportHost2()

			dt1, err := NewDataTransfer(gsData.DtDs1, gsData.TempDir1, gsData.DtNet1, tp1, gsData.StoredCounter1)
			require.NoError(t, err)
			testutil.StartAndWaitForReady(ctx, t, dt1)
			dt2, err := NewDataTransfer(gsData.DtDs2, gsData.TempDir2, gsData.DtNet2, tp2, gsData.StoredCounter2)
			require.NoError(t, err)
			testutil.StartAndWaitForReady(ctx, t, dt2)

			finished := make(chan struct{}, 2*data.requestCount)
			errChan := make(chan string, 2*data.requestCount)
			opened := make(chan struct{}, 2*data.requestCount)
			var subscriber datatransfer.Subscriber = func(event datatransfer.Event, channelState datatransfer.ChannelState) {
				if channelState.Status() == datatransfer.Completed {
					finished <- struct{}{}
				}
				if event.Code == datatransfer.Error {
					errChan <- event.Message
				}
				if event.Code == datatransfer.Open {
					opened <- struct{}{}
				}
			}
			dt1.SubscribeToEvents(subscriber)
			dt2.SubscribeToEvents(subscriber)
			vouchers := make([]datatransfer.Voucher, 0, data.requestCount)
			for i := 0; i < data.requestCount; i++ {
				vouchers = append(vouchers, testutil.NewFakeDTType())
			}
			sv := testutil.NewStubbedValidator()

			root, origBytes := testutil.LoadUnixFSFile(ctx, t, gsData.DagService1, loremFile)
			rootCid := root.(cidlink.Link).Cid

			destDagServices := make([]ipldformat.DAGService, 0, data.requestCount)
			loaders := make([]ipld.Loader, 0, data.requestCount)
			storers := make([]ipld.Storer, 0, data.requestCount)
			for i := 0; i < data.requestCount; i++ {
				ds := dss.MutexWrap(datastore.NewMapDatastore())
				bs := bstore.NewBlockstore(namespace.Wrap(ds, datastore.NewKey("blockstore")))
				loader := storeutil.LoaderForBlockstore(bs)
				storer := storeutil.StorerForBlockstore(bs)
				destDagService := merkledag.NewDAGService(blockservice.New(bs, offline.Exchange(bs)))

				destDagServices = append(destDagServices, destDagService)
				loaders = append(loaders, loader)
				storers = append(storers, storer)
			}

			err = dt2.RegisterTransportConfigurer(&testutil.FakeDTType{}, func(channelID datatransfer.ChannelID, testVoucher datatransfer.Voucher, transport datatransfer.Transport) {
				fv, ok := testVoucher.(*testutil.FakeDTType)
				if ok {
					for i, voucher := range vouchers {
						if fv.Data == voucher.(*testutil.FakeDTType).Data {
							gsTransport, ok := transport.(*tp.Transport)
							if ok {
								err := gsTransport.UseStore(channelID, loaders[i], storers[i])
								require.NoError(t, err)
							}
						}
					}
				}
			})
			require.NoError(t, err)

			if data.isPull {
				sv.ExpectSuccessPull()
				require.NoError(t, dt1.RegisterVoucherType(&testutil.FakeDTType{}, sv))
				for i := 0; i < data.requestCount; i++ {
					_, err = dt2.OpenPullDataChannel(ctx, host1.ID(), vouchers[i], rootCid, gsData.AllSelector)
					require.NoError(t, err)
				}
			} else {
				sv.ExpectSuccessPush()
				require.NoError(t, dt2.RegisterVoucherType(&testutil.FakeDTType{}, sv))
				for i := 0; i < data.requestCount; i++ {
					_, err = dt1.OpenPushDataChannel(ctx, host2.ID(), vouchers[i], rootCid, gsData.AllSelector)
					require.NoError(t, err)
				}
			}
			opens := 0
			completes := 0
			for opens < 2*data.requestCount || completes < 2*data.requestCount {
				select {
				case <-ctx.Done():
					t.Fatal("Did not complete successful data transfer")
				case <-finished:
					completes++
				case <-opened:
					opens++
				case err := <-errChan:
					t.Fatalf("received error on data transfer: %s", err)
				}
			}
			for _, destDagService := range destDagServices {
				testutil.VerifyHasFile(ctx, t, destDagService, root, origBytes)
			}
		})
	}
}

func TestManyReceiversAtOnce(t *testing.T) {
	ctx := context.Background()
	testCases := map[string]struct {
		isPull        bool
		receiverCount int
	}{
		"multiple receivers for push requests": {
			receiverCount: 10,
		},
		"multiple receivers for pull requests": {
			isPull:        true,
			receiverCount: 10,
		},
	}
	for testCase, data := range testCases {
		t.Run(testCase, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			gsData := testutil.NewGraphsyncTestingData(ctx, t, nil, nil)
			host1 := gsData.Host1 // initiator, data sender

			tp1 := gsData.SetupGSTransportHost1()
			dt1, err := NewDataTransfer(gsData.DtDs1, gsData.TempDir1, gsData.DtNet1, tp1, gsData.StoredCounter1)
			require.NoError(t, err)
			testutil.StartAndWaitForReady(ctx, t, dt1)

			destDagServices := make([]ipldformat.DAGService, 0, data.receiverCount)
			receivers := make([]datatransfer.Manager, 0, data.receiverCount)
			hosts := make([]host.Host, 0, data.receiverCount)
			for i := 0; i < data.receiverCount; i++ {
				host, err := gsData.Mn.GenPeer()
				require.NoError(t, err, "error generating host")
				gsnet := gsnet.NewFromLibp2pHost(host)
				dtnet := network.NewFromLibp2pHost(host)
				ds := dss.MutexWrap(datastore.NewMapDatastore())
				bs := bstore.NewBlockstore(namespace.Wrap(ds, datastore.NewKey("blockstore")))
				altBs := bstore.NewBlockstore(namespace.Wrap(ds, datastore.NewKey("altstore")))

				loader := storeutil.LoaderForBlockstore(bs)
				storer := storeutil.StorerForBlockstore(bs)
				altLoader := storeutil.LoaderForBlockstore(altBs)
				altStorer := storeutil.StorerForBlockstore(altBs)

				destDagService := merkledag.NewDAGService(blockservice.New(altBs, offline.Exchange(altBs)))

				gs := gsimpl.New(gsData.Ctx, gsnet, loader, storer)
				gsTransport := tp.NewTransport(host.ID(), gs)

				dtDs := namespace.Wrap(ds, datastore.NewKey("datatransfer"))

				storedCounter := storedcounter.New(ds, datastore.NewKey("counter"))

				receiver, err := NewDataTransfer(dtDs, os.TempDir(), dtnet, gsTransport, storedCounter)
				require.NoError(t, err)
				err = receiver.Start(gsData.Ctx)
				require.NoError(t, err)

				err = receiver.RegisterTransportConfigurer(&testutil.FakeDTType{}, func(channelID datatransfer.ChannelID, testVoucher datatransfer.Voucher, transport datatransfer.Transport) {
					_, isFv := testVoucher.(*testutil.FakeDTType)
					gsTransport, isGs := transport.(*tp.Transport)
					if isFv && isGs {
						err := gsTransport.UseStore(channelID, altLoader, altStorer)
						require.NoError(t, err)
					}
				})
				require.NoError(t, err)

				destDagServices = append(destDagServices, destDagService)
				receivers = append(receivers, receiver)
				hosts = append(hosts, host)
			}
			err = gsData.Mn.LinkAll()
			require.NoError(t, err, "error linking hosts")

			finished := make(chan struct{}, 2*data.receiverCount)
			errChan := make(chan string, 2*data.receiverCount)
			opened := make(chan struct{}, 2*data.receiverCount)
			var subscriber datatransfer.Subscriber = func(event datatransfer.Event, channelState datatransfer.ChannelState) {
				if channelState.Status() == datatransfer.Completed {
					finished <- struct{}{}
				}
				if event.Code == datatransfer.Error {
					errChan <- event.Message
				}
				if event.Code == datatransfer.Open {
					opened <- struct{}{}
				}
			}
			dt1.SubscribeToEvents(subscriber)
			for _, receiver := range receivers {
				receiver.SubscribeToEvents(subscriber)
			}
			vouchers := make([]datatransfer.Voucher, 0, data.receiverCount)
			for i := 0; i < data.receiverCount; i++ {
				vouchers = append(vouchers, testutil.NewFakeDTType())
			}
			sv := testutil.NewStubbedValidator()

			root, origBytes := testutil.LoadUnixFSFile(ctx, t, gsData.DagService1, loremFile)
			rootCid := root.(cidlink.Link).Cid

			if data.isPull {
				sv.ExpectSuccessPull()
				require.NoError(t, dt1.RegisterVoucherType(&testutil.FakeDTType{}, sv))
				for i, receiver := range receivers {
					_, err = receiver.OpenPullDataChannel(ctx, host1.ID(), vouchers[i], rootCid, gsData.AllSelector)
					require.NoError(t, err)
				}
			} else {
				sv.ExpectSuccessPush()
				for i, receiver := range receivers {
					require.NoError(t, receiver.RegisterVoucherType(&testutil.FakeDTType{}, sv))
					_, err = dt1.OpenPushDataChannel(ctx, hosts[i].ID(), vouchers[i], rootCid, gsData.AllSelector)
					require.NoError(t, err)
				}
			}
			opens := 0
			completes := 0
			for opens < 2*data.receiverCount || completes < 2*data.receiverCount {
				select {
				case <-ctx.Done():
					t.Fatal("Did not complete successful data transfer")
				case <-finished:
					completes++
				case <-opened:
					opens++
				case err := <-errChan:
					t.Fatalf("received error on data transfer: %s", err)
				}
			}
			for _, destDagService := range destDagServices {
				testutil.VerifyHasFile(ctx, t, destDagService, root, origBytes)
			}
		})
	}
}

// TestPushRequestAutoRestart tests that if the connection for a push request
// goes down, it will automatically restart (given the right config options)
func TestPushRequestAutoRestart(t *testing.T) {
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	gsData := testutil.NewGraphsyncTestingData(ctx, t, nil, nil)
	netRetry := network.RetryParameters(time.Second, time.Second, 5, 1)
	gsData.DtNet1 = network.NewFromLibp2pHost(gsData.Host1, netRetry)
	host1 := gsData.Host1 // initiator, data sender
	host2 := gsData.Host2 // data recipient

	tp1 := gsData.SetupGSTransportHost1()
	tp2 := gsData.SetupGSTransportHost2()

	restartConf := PushChannelRestartConfig(100*time.Millisecond, 1, 10, 200*time.Millisecond, 5)
	dt1, err := NewDataTransfer(gsData.DtDs1, gsData.TempDir1, gsData.DtNet1, tp1, gsData.StoredCounter1, restartConf)
	require.NoError(t, err)
	testutil.StartAndWaitForReady(ctx, t, dt1)
	dt2, err := NewDataTransfer(gsData.DtDs2, gsData.TempDir2, gsData.DtNet2, tp2, gsData.StoredCounter2)
	require.NoError(t, err)
	testutil.StartAndWaitForReady(ctx, t, dt2)

	received := make(chan struct{})
	finished := make(chan struct{}, 2)
	var subscriber datatransfer.Subscriber = func(event datatransfer.Event, channelState datatransfer.ChannelState) {
		//t.Logf("%s: %s\n", datatransfer.Events[event.Code], datatransfer.Statuses[channelState.Status()])

		if event.Code == datatransfer.DataReceived {
			received <- struct{}{}
		}

		if channelState.Status() == datatransfer.Completed {
			finished <- struct{}{}
		}
	}
	dt1.SubscribeToEvents(subscriber)
	dt2.SubscribeToEvents(subscriber)
	voucher := testutil.FakeDTType{Data: "applesauce"}
	sv := testutil.NewStubbedValidator()

	sourceDagService := gsData.DagService1
	destDagService := gsData.DagService2

	root, origBytes := testutil.LoadUnixFSFile(ctx, t, sourceDagService, loremFile)
	rootCid := root.(cidlink.Link).Cid

	require.NoError(t, dt1.RegisterVoucherType(&testutil.FakeDTType{}, sv))
	require.NoError(t, dt2.RegisterVoucherType(&testutil.FakeDTType{}, sv))
	chid, err := dt1.OpenPushDataChannel(ctx, host2.ID(), &voucher, rootCid, gsData.AllSelector)
	require.NoError(t, err)

	// Wait for a block to be received
	<-received

	// Break connection
	t.Logf("Breaking connection to peer")
	require.NoError(t, gsData.Mn.UnlinkPeers(host1.ID(), host2.ID()))
	require.NoError(t, gsData.Mn.DisconnectPeers(host1.ID(), host2.ID()))

	t.Logf("Sleep for a second")
	time.Sleep(1 * time.Second)

	// Restore connection
	t.Logf("Restore connection")
	require.NoError(t, gsData.Mn.LinkAll())
	time.Sleep(200 * time.Millisecond)
	conn, err := gsData.Mn.ConnectPeers(host1.ID(), host2.ID())
	require.NoError(t, err)
	require.NotNil(t, conn)

	t.Logf("Waiting for auto-restart on push channel %s", chid)

	(func() {
		finishedCount := 0
		for {
			select {
			case <-ctx.Done():
				t.Fatal("Did not complete successful data transfer")
				return
			case <-received:
			case <-finished:
				finishedCount++
				if finishedCount == 2 {
					return
				}
			}
		}
	})()

	// Verify that the file was transferred to the destination node
	testutil.VerifyHasFile(ctx, t, destDagService, root, origBytes)
}

func TestRoundTripCancelledRequest(t *testing.T) {
	ctx := context.Background()
	testCases := map[string]struct {
		isPull bool
	}{
		"cancelled push request": {},
		"cancelled pull request": {
			isPull: true,
		},
	}
	for testCase, data := range testCases {
		t.Run(testCase, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			gsData := testutil.NewGraphsyncTestingData(ctx, t, nil, nil)
			host1 := gsData.Host1 // initiator, data sender
			host2 := gsData.Host2

			tp1 := gsData.SetupGSTransportHost1()
			tp2 := gsData.SetupGSTransportHost2()

			dt1, err := NewDataTransfer(gsData.DtDs1, gsData.TempDir1, gsData.DtNet1, tp1, gsData.StoredCounter1)
			require.NoError(t, err)
			testutil.StartAndWaitForReady(ctx, t, dt1)
			dt2, err := NewDataTransfer(gsData.DtDs2, gsData.TempDir2, gsData.DtNet2, tp2, gsData.StoredCounter2)
			require.NoError(t, err)
			testutil.StartAndWaitForReady(ctx, t, dt2)

			finished := make(chan struct{}, 2)
			errChan := make(chan string, 2)
			cancelled := make(chan struct{}, 2)
			accepted := make(chan struct{}, 2)
			opened := make(chan struct{}, 2)
			var subscriber datatransfer.Subscriber = func(event datatransfer.Event, channelState datatransfer.ChannelState) {
				if channelState.Status() == datatransfer.Completed {
					finished <- struct{}{}
				}
				if event.Code == datatransfer.Accept {
					accepted <- struct{}{}
				}
				if event.Code == datatransfer.Error {
					errChan <- event.Message
				}
				if event.Code == datatransfer.Cancel {
					cancelled <- struct{}{}
				}
				if event.Code == datatransfer.Open {
					opened <- struct{}{}
				}
			}
			dt1.SubscribeToEvents(subscriber)
			dt2.SubscribeToEvents(subscriber)
			voucher := testutil.FakeDTType{Data: "applesauce"}
			sv := testutil.NewStubbedValidator()
			root, _ := testutil.LoadUnixFSFile(ctx, t, gsData.DagService1, loremFile)
			rootCid := root.(cidlink.Link).Cid

			var chid datatransfer.ChannelID
			if data.isPull {
				sv.ExpectPausePull()
				require.NoError(t, dt1.RegisterVoucherType(&testutil.FakeDTType{}, sv))
				chid, err = dt2.OpenPullDataChannel(ctx, host1.ID(), &voucher, rootCid, gsData.AllSelector)
			} else {
				sv.ExpectPausePush()
				require.NoError(t, dt2.RegisterVoucherType(&testutil.FakeDTType{}, sv))
				chid, err = dt1.OpenPushDataChannel(ctx, host2.ID(), &voucher, rootCid, gsData.AllSelector)
			}
			require.NoError(t, err)
			opens := 0
			cancels := 0
			accepts := 0
			for opens < 2 || cancels < 2 {
				select {
				case <-ctx.Done():
					t.Fatal("Did not finish data transfer")
				case <-finished:
					t.Fatal("request completed succussfully but should have been cancelled")
				case <-opened:
					opens++
				case <-cancelled:
					cancels++
				case <-accepted:
					if accepts == 0 {
						timer := time.NewTimer(10 * time.Millisecond)
						go func() {
							select {
							case <-ctx.Done():
							case <-timer.C:
								if data.isPull {
									_ = dt1.CloseDataTransferChannel(ctx, chid)
								} else {
									_ = dt2.CloseDataTransferChannel(ctx, chid)
								}
							}
						}()
					}
					accepts++
				case err := <-errChan:
					t.Fatalf("received error on data transfer: %s", err)
				}
			}
		})
	}
}

type retrievalRevalidator struct {
	*testutil.StubbedRevalidator
	dataSoFar          uint64
	providerPausePoint int
	pausePoints        []uint64
	finalVoucher       datatransfer.VoucherResult
}

func (r *retrievalRevalidator) OnPullDataSent(chid datatransfer.ChannelID, additionalBytesSent uint64) (bool, datatransfer.VoucherResult, error) {
	r.dataSoFar += additionalBytesSent
	if r.providerPausePoint < len(r.pausePoints) &&
		r.dataSoFar >= r.pausePoints[r.providerPausePoint] {
		r.providerPausePoint++
		return true, testutil.NewFakeDTType(), datatransfer.ErrPause
	}
	return true, nil, nil
}

func (r *retrievalRevalidator) OnPushDataReceived(chid datatransfer.ChannelID, additionalBytesReceived uint64) (bool, datatransfer.VoucherResult, error) {
	return false, nil, nil
}
func (r *retrievalRevalidator) OnComplete(chid datatransfer.ChannelID) (bool, datatransfer.VoucherResult, error) {
	return true, r.finalVoucher, datatransfer.ErrPause
}

func TestSimulatedRetrievalFlow(t *testing.T) {
	ctx := context.Background()
	testCases := map[string]struct {
		unpauseRequestorDelay time.Duration
		unpauseResponderDelay time.Duration
		pausePoints           []uint64
	}{
		"fast unseal, payment channel ready": {
			pausePoints: []uint64{1000, 3000, 6000, 10000, 15000},
		},
		"fast unseal, payment channel not ready": {
			unpauseRequestorDelay: 100 * time.Millisecond,
			pausePoints:           []uint64{1000, 3000, 6000, 10000, 15000},
		},
		"slow unseal, payment channel ready": {
			unpauseResponderDelay: 200 * time.Millisecond,
			pausePoints:           []uint64{1000, 3000, 6000, 10000, 15000},
		},
	}
	for testCase, config := range testCases {
		t.Run(testCase, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
			defer cancel()

			gsData := testutil.NewGraphsyncTestingData(ctx, t, nil, nil)
			host1 := gsData.Host1 // initiator, data sender

			root := gsData.LoadUnixFSFile(t, false)
			rootCid := root.(cidlink.Link).Cid
			tp1 := gsData.SetupGSTransportHost1()
			tp2 := gsData.SetupGSTransportHost2()

			dt1, err := NewDataTransfer(gsData.DtDs1, gsData.TempDir1, gsData.DtNet1, tp1, gsData.StoredCounter1)
			require.NoError(t, err)
			testutil.StartAndWaitForReady(ctx, t, dt1)
			dt2, err := NewDataTransfer(gsData.DtDs2, gsData.TempDir2, gsData.DtNet2, tp2, gsData.StoredCounter2)
			require.NoError(t, err)
			testutil.StartAndWaitForReady(ctx, t, dt2)
			var chid datatransfer.ChannelID
			errChan := make(chan struct{}, 2)
			clientPausePoint := 0
			clientFinished := make(chan struct{}, 1)
			finalVoucherResult := testutil.NewFakeDTType()
			encodedFVR, err := encoding.Encode(finalVoucherResult)
			require.NoError(t, err)
			var clientSubscriber datatransfer.Subscriber = func(event datatransfer.Event, channelState datatransfer.ChannelState) {
				if event.Code == datatransfer.Error {
					errChan <- struct{}{}
				}
				if event.Code == datatransfer.NewVoucherResult {
					lastVoucherResult := channelState.LastVoucherResult()
					encodedLVR, err := encoding.Encode(lastVoucherResult)
					require.NoError(t, err)
					if bytes.Equal(encodedLVR, encodedFVR) {
						_ = dt2.SendVoucher(ctx, chid, testutil.NewFakeDTType())
					}
				}

				if event.Code == datatransfer.DataReceived &&
					clientPausePoint < len(config.pausePoints) &&
					channelState.Received() > config.pausePoints[clientPausePoint] {
					_ = dt2.SendVoucher(ctx, chid, testutil.NewFakeDTType())
					clientPausePoint++
				}
				if channelState.Status() == datatransfer.Completed {
					clientFinished <- struct{}{}
				}
			}
			dt2.SubscribeToEvents(clientSubscriber)
			providerFinished := make(chan struct{}, 1)
			providerAccepted := false
			var providerSubscriber datatransfer.Subscriber = func(event datatransfer.Event, channelState datatransfer.ChannelState) {
				if event.Code == datatransfer.PauseResponder {
					if !providerAccepted {
						providerAccepted = true
						timer := time.NewTimer(config.unpauseResponderDelay)
						go func() {
							<-timer.C
							_ = dt1.ResumeDataTransferChannel(ctx, chid)
						}()
					}
				}
				if event.Code == datatransfer.Error {
					errChan <- struct{}{}
				}
				if channelState.Status() == datatransfer.Completed {
					providerFinished <- struct{}{}
				}
			}
			dt1.SubscribeToEvents(providerSubscriber)
			voucher := testutil.FakeDTType{Data: "applesauce"}
			sv := testutil.NewStubbedValidator()
			sv.ExpectPausePull()
			require.NoError(t, dt1.RegisterVoucherType(&testutil.FakeDTType{}, sv))

			srv := &retrievalRevalidator{
				testutil.NewStubbedRevalidator(), 0, 0, config.pausePoints, finalVoucherResult,
			}
			srv.ExpectSuccessRevalidation()
			require.NoError(t, dt1.RegisterRevalidator(testutil.NewFakeDTType(), srv))

			require.NoError(t, dt2.RegisterVoucherResultType(testutil.NewFakeDTType()))
			chid, err = dt2.OpenPullDataChannel(ctx, host1.ID(), &voucher, rootCid, gsData.AllSelector)
			require.NoError(t, err)

			for providerFinished != nil || clientFinished != nil {
				select {
				case <-ctx.Done():
					t.Fatal("Did not complete successful data transfer")
				case <-providerFinished:
					providerFinished = nil
				case <-clientFinished:
					clientFinished = nil
				case <-errChan:
					t.Fatal("received unexpected error")
				}
			}
			sv.VerifyExpectations(t)
			srv.VerifyExpectations(t)
			gsData.VerifyFileTransferred(t, root, true)
			require.Equal(t, srv.providerPausePoint, len(config.pausePoints))
			require.Equal(t, clientPausePoint, len(config.pausePoints))
		})
	}
}

func TestPauseAndResume(t *testing.T) {
	ctx := context.Background()
	testCases := map[string]bool{
		"pause and resume works for push requests": false,
		"pause and resume works for pull requests": true,
	}
	for testCase, isPull := range testCases {
		t.Run(testCase, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			gsData := testutil.NewGraphsyncTestingData(ctx, t, nil, nil)
			host1 := gsData.Host1 // initiator, data sender
			host2 := gsData.Host2 // data recipient

			root := gsData.LoadUnixFSFile(t, false)
			rootCid := root.(cidlink.Link).Cid
			tp1 := gsData.SetupGSTransportHost1()
			tp2 := gsData.SetupGSTransportHost2()

			dt1, err := NewDataTransfer(gsData.DtDs1, gsData.TempDir1, gsData.DtNet1, tp1, gsData.StoredCounter1)
			require.NoError(t, err)
			testutil.StartAndWaitForReady(ctx, t, dt1)
			dt2, err := NewDataTransfer(gsData.DtDs2, gsData.TempDir2, gsData.DtNet2, tp2, gsData.StoredCounter2)
			require.NoError(t, err)
			testutil.StartAndWaitForReady(ctx, t, dt2)
			finished := make(chan struct{}, 2)
			errChan := make(chan struct{}, 2)
			opened := make(chan struct{}, 2)
			sent := make(chan uint64, 100)
			received := make(chan uint64, 100)
			pauseInitiator := make(chan struct{}, 2)
			resumeInitiator := make(chan struct{}, 2)
			pauseResponder := make(chan struct{}, 2)
			resumeResponder := make(chan struct{}, 2)
			var subscriber datatransfer.Subscriber = func(event datatransfer.Event, channelState datatransfer.ChannelState) {

				if event.Code == datatransfer.DataQueued {
					if channelState.Queued() > 0 {
						sent <- channelState.Queued()
					}
				}

				if event.Code == datatransfer.DataReceived {
					if channelState.Received() > 0 {
						received <- channelState.Received()
					}
				}

				if event.Code == datatransfer.PauseInitiator {
					pauseInitiator <- struct{}{}
				}
				if event.Code == datatransfer.ResumeInitiator {
					resumeInitiator <- struct{}{}
				}
				if event.Code == datatransfer.PauseResponder {
					pauseResponder <- struct{}{}
				}
				if event.Code == datatransfer.ResumeResponder {
					resumeResponder <- struct{}{}
				}
				if channelState.Status() == datatransfer.Completed {
					finished <- struct{}{}
				}
				if event.Code == datatransfer.Error {
					errChan <- struct{}{}
				}
				if event.Code == datatransfer.Open {
					opened <- struct{}{}
				}
			}
			dt1.SubscribeToEvents(subscriber)
			dt2.SubscribeToEvents(subscriber)
			voucher := testutil.FakeDTType{Data: "applesauce"}
			sv := testutil.NewStubbedValidator()

			var chid datatransfer.ChannelID
			if isPull {
				sv.ExpectSuccessPull()
				require.NoError(t, dt1.RegisterVoucherType(&testutil.FakeDTType{}, sv))
				chid, err = dt2.OpenPullDataChannel(ctx, host1.ID(), &voucher, rootCid, gsData.AllSelector)
			} else {
				sv.ExpectSuccessPush()
				require.NoError(t, dt2.RegisterVoucherType(&testutil.FakeDTType{}, sv))
				chid, err = dt1.OpenPushDataChannel(ctx, host2.ID(), &voucher, rootCid, gsData.AllSelector)
			}
			require.NoError(t, err)
			opens := 0
			completes := 0
			pauseInitiators := 0
			pauseResponders := 0
			resumeInitiators := 0
			resumeResponders := 0
			sentIncrements := make([]uint64, 0, 21)
			receivedIncrements := make([]uint64, 0, 21)
			for opens < 2 || completes < 2 || len(sentIncrements) < 21 || len(receivedIncrements) < 21 ||
				pauseInitiators < 1 || pauseResponders < 1 || resumeInitiators < 1 || resumeResponders < 1 {
				select {
				case <-ctx.Done():
					t.Fatal("Did not complete successful data transfer")
				case <-finished:
					completes++
				case <-opened:
					opens++
				case <-pauseInitiator:
					pauseInitiators++
				case <-resumeInitiator:
					resumeInitiators++
				case <-pauseResponder:
					pauseResponders++
				case <-resumeResponder:
					resumeResponders++
				case sentIncrement := <-sent:
					sentIncrements = append(sentIncrements, sentIncrement)
					if len(sentIncrements) == 5 {
						require.NoError(t, dt1.PauseDataTransferChannel(ctx, chid))
						time.Sleep(100 * time.Millisecond)
						require.NoError(t, dt1.ResumeDataTransferChannel(ctx, chid))
					}
				case receivedIncrement := <-received:
					receivedIncrements = append(receivedIncrements, receivedIncrement)
					if len(receivedIncrements) == 10 {
						require.NoError(t, dt2.PauseDataTransferChannel(ctx, chid))
						time.Sleep(100 * time.Millisecond)
						require.NoError(t, dt2.ResumeDataTransferChannel(ctx, chid))
					}
				case <-errChan:
					t.Fatal("received error on data transfer")
				}
			}
			require.Equal(t, sentIncrements, receivedIncrements)
			gsData.VerifyFileTransferred(t, root, true)
			if isPull {
				assert.Equal(t, chid.Initiator, host2.ID())
			} else {
				assert.Equal(t, chid.Initiator, host1.ID())
			}
		})
	}
}

func TestUnrecognizedVoucherRoundTrip(t *testing.T) {
	ctx := context.Background()
	testCases := map[string]bool{
		"push requests": false,
		"pull requests": true,
	}
	for testCase, isPull := range testCases {
		t.Run(testCase, func(t *testing.T) {
			//	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			//	defer cancel()

			gsData := testutil.NewGraphsyncTestingData(ctx, t, nil, nil)
			host1 := gsData.Host1 // initiator, data sender
			host2 := gsData.Host2 // data recipient

			tp1 := gsData.SetupGSTransportHost1()
			tp2 := gsData.SetupGSTransportHost2()

			dt1, err := NewDataTransfer(gsData.DtDs1, gsData.TempDir1, gsData.DtNet1, tp1, gsData.StoredCounter1)
			require.NoError(t, err)
			testutil.StartAndWaitForReady(ctx, t, dt1)
			dt2, err := NewDataTransfer(gsData.DtDs2, gsData.TempDir2, gsData.DtNet2, tp2, gsData.StoredCounter2)
			require.NoError(t, err)
			testutil.StartAndWaitForReady(ctx, t, dt2)

			finished := make(chan struct{}, 2)
			errChan := make(chan string, 2)
			opened := make(chan struct{}, 2)
			var subscriber datatransfer.Subscriber = func(event datatransfer.Event, channelState datatransfer.ChannelState) {
				if channelState.Status() == datatransfer.Failed {
					finished <- struct{}{}
				}
				if event.Code == datatransfer.Error {
					errChan <- channelState.Message()
				}
				if event.Code == datatransfer.Open {
					opened <- struct{}{}
				}
			}
			dt1.SubscribeToEvents(subscriber)
			dt2.SubscribeToEvents(subscriber)
			voucher := testutil.FakeDTType{Data: "applesauce"}

			root, _ := testutil.LoadUnixFSFile(ctx, t, gsData.DagService1, loremFile)
			rootCid := root.(cidlink.Link).Cid

			if isPull {
				_, err = dt2.OpenPullDataChannel(ctx, host1.ID(), &voucher, rootCid, gsData.AllSelector)
			} else {
				_, err = dt1.OpenPushDataChannel(ctx, host2.ID(), &voucher, rootCid, gsData.AllSelector)
			}
			require.NoError(t, err)
			opens := 0
			var errMessages []string
			finishes := 0
			for opens < 1 || finishes < 1 {
				select {
				case <-ctx.Done():
					t.Fatal("Did not complete successful data transfer")
				case <-finished:
					finishes++
				case <-opened:
					opens++
				case errMessage := <-errChan:
					require.Equal(t, errMessage, datatransfer.ErrRejected.Error())
					errMessages = append(errMessages, errMessage)
					if len(errMessages) > 1 {
						t.Fatal("too many errors")
					}
				}
			}
		})
	}
}

func TestDataTransferSubscribing(t *testing.T) {
	// create network
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	gsData := testutil.NewGraphsyncTestingData(ctx, t, nil, nil)
	host2 := gsData.Host2

	tp1 := gsData.SetupGSTransportHost1()
	tp2 := gsData.SetupGSTransportHost2()
	sv := testutil.NewStubbedValidator()
	sv.StubErrorPull()
	sv.StubErrorPush()
	dt2, err := NewDataTransfer(gsData.DtDs2, gsData.TempDir2, gsData.DtNet2, tp2, gsData.StoredCounter2)
	require.NoError(t, err)
	testutil.StartAndWaitForReady(ctx, t, dt2)
	require.NoError(t, dt2.RegisterVoucherType(&testutil.FakeDTType{}, sv))
	voucher := testutil.FakeDTType{Data: "applesauce"}
	baseCid := testutil.GenerateCids(1)[0]

	dt1, err := NewDataTransfer(gsData.DtDs1, gsData.TempDir1, gsData.DtNet1, tp1, gsData.StoredCounter1)
	require.NoError(t, err)
	testutil.StartAndWaitForReady(ctx, t, dt1)
	subscribe1Calls := make(chan struct{}, 1)
	subscribe1 := func(event datatransfer.Event, channelState datatransfer.ChannelState) {
		if event.Code == datatransfer.Error {
			subscribe1Calls <- struct{}{}
		}
	}
	subscribe2Calls := make(chan struct{}, 1)
	subscribe2 := func(event datatransfer.Event, channelState datatransfer.ChannelState) {
		if event.Code == datatransfer.Error {
			subscribe2Calls <- struct{}{}
		}
	}
	unsub1 := dt1.SubscribeToEvents(subscribe1)
	unsub2 := dt1.SubscribeToEvents(subscribe2)
	_, err = dt1.OpenPushDataChannel(ctx, host2.ID(), &voucher, baseCid, gsData.AllSelector)
	require.NoError(t, err)
	select {
	case <-ctx.Done():
		t.Fatal("subscribed events not received")
	case <-subscribe1Calls:
	}
	select {
	case <-ctx.Done():
		t.Fatal("subscribed events not received")
	case <-subscribe2Calls:
	}
	unsub1()
	unsub2()

	subscribe3Calls := make(chan struct{}, 1)
	subscribe3 := func(event datatransfer.Event, channelState datatransfer.ChannelState) {
		if event.Code == datatransfer.Error {
			subscribe3Calls <- struct{}{}
		}
	}
	subscribe4Calls := make(chan struct{}, 1)
	subscribe4 := func(event datatransfer.Event, channelState datatransfer.ChannelState) {
		if event.Code == datatransfer.Error {
			subscribe4Calls <- struct{}{}
		}
	}
	unsub3 := dt1.SubscribeToEvents(subscribe3)
	unsub4 := dt1.SubscribeToEvents(subscribe4)
	_, err = dt1.OpenPullDataChannel(ctx, host2.ID(), &voucher, baseCid, gsData.AllSelector)
	require.NoError(t, err)
	select {
	case <-ctx.Done():
		t.Fatal("subscribed events not received")
	case <-subscribe1Calls:
		t.Fatal("received channel that should have been unsubscribed")
	case <-subscribe2Calls:
		t.Fatal("received channel that should have been unsubscribed")
	case <-subscribe3Calls:
	}
	select {
	case <-ctx.Done():
		t.Fatal("subscribed events not received")
	case <-subscribe1Calls:
		t.Fatal("received channel that should have been unsubscribed")
	case <-subscribe2Calls:
		t.Fatal("received channel that should have been unsubscribed")
	case <-subscribe4Calls:
	}
	unsub3()
	unsub4()
}

type receivedGraphSyncMessage struct {
	message gsmsg.GraphSyncMessage
	p       peer.ID
}

type fakeGraphSyncReceiver struct {
	receivedMessages chan receivedGraphSyncMessage
}

func (fgsr *fakeGraphSyncReceiver) ReceiveMessage(ctx context.Context, sender peer.ID, incoming gsmsg.GraphSyncMessage) {
	select {
	case <-ctx.Done():
	case fgsr.receivedMessages <- receivedGraphSyncMessage{incoming, sender}:
	}
}

func (fgsr *fakeGraphSyncReceiver) ReceiveError(_ peer.ID, _ error) {
}
func (fgsr *fakeGraphSyncReceiver) Connected(p peer.ID) {
}
func (fgsr *fakeGraphSyncReceiver) Disconnected(p peer.ID) {
}

func (fgsr *fakeGraphSyncReceiver) consumeResponses(ctx context.Context, t *testing.T) graphsync.ResponseStatusCode {
	var gsMessageReceived receivedGraphSyncMessage
	for {
		select {
		case <-ctx.Done():
			t.Fail()
		case gsMessageReceived = <-fgsr.receivedMessages:
			responses := gsMessageReceived.message.Responses()
			if (len(responses) > 0) && gsmsg.IsTerminalResponseCode(responses[0].Status()) {
				return responses[0].Status()
			}
		}
	}
}

func TestRespondingToPushGraphsyncRequests(t *testing.T) {
	// create network
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	gsData := testutil.NewGraphsyncTestingData(ctx, t, nil, nil)
	host1 := gsData.Host1 // initiator and data sender
	host2 := gsData.Host2 // data recipient, makes graphsync request for data
	voucher := testutil.NewFakeDTType()
	link := gsData.LoadUnixFSFile(t, false)

	// setup receiving peer to just record message coming in
	dtnet2 := network.NewFromLibp2pHost(host2)
	r := &receiver{
		messageReceived: make(chan receivedMessage),
	}
	dtnet2.SetDelegate(r)

	gsr := &fakeGraphSyncReceiver{
		receivedMessages: make(chan receivedGraphSyncMessage),
	}
	gsData.GsNet2.SetDelegate(gsr)

	tp1 := gsData.SetupGSTransportHost1()
	dt1, err := NewDataTransfer(gsData.DtDs1, gsData.TempDir1, gsData.DtNet1, tp1, gsData.StoredCounter1)
	require.NoError(t, err)
	testutil.StartAndWaitForReady(ctx, t, dt1)
	voucherResult := testutil.NewFakeDTType()
	err = dt1.RegisterVoucherResultType(voucherResult)
	require.NoError(t, err)

	t.Run("when request is initiated", func(t *testing.T) {
		_, err := dt1.OpenPushDataChannel(ctx, host2.ID(), voucher, link.(cidlink.Link).Cid, gsData.AllSelector)
		require.NoError(t, err)

		var messageReceived receivedMessage
		select {
		case <-ctx.Done():
			t.Fatal("did not receive message sent")
		case messageReceived = <-r.messageReceived:
		}
		requestReceived := messageReceived.message.(datatransfer.Request)

		var buf bytes.Buffer
		response, err := message.NewResponse(requestReceived.TransferID(), true, false, voucherResult.Type(), voucherResult)
		require.NoError(t, err)
		err = response.ToNet(&buf)
		require.NoError(t, err)
		extData := buf.Bytes()

		request := gsmsg.NewRequest(graphsync.RequestID(rand.Int31()), link.(cidlink.Link).Cid, gsData.AllSelector, graphsync.Priority(rand.Int31()), graphsync.ExtensionData{
			Name: extension.ExtensionDataTransfer1_1,
			Data: extData,
		})
		builder := gsmsg.NewBuilder(0)
		builder.AddRequest(request)
		gsmessage, err := builder.Build()
		require.NoError(t, err)
		require.NoError(t, gsData.GsNet2.SendMessage(ctx, host1.ID(), gsmessage))

		status := gsr.consumeResponses(ctx, t)
		require.False(t, gsmsg.IsTerminalFailureCode(status))
	})

	t.Run("when no request is initiated", func(t *testing.T) {
		var buf bytes.Buffer
		response, err := message.NewResponse(datatransfer.TransferID(rand.Uint64()), true, false, voucher.Type(), voucher)
		require.NoError(t, err)
		err = response.ToNet(&buf)
		require.NoError(t, err)
		extData := buf.Bytes()

		request := gsmsg.NewRequest(graphsync.RequestID(rand.Int31()), link.(cidlink.Link).Cid, gsData.AllSelector, graphsync.Priority(rand.Int31()), graphsync.ExtensionData{
			Name: extension.ExtensionDataTransfer1_1,
			Data: extData,
		})
		builder := gsmsg.NewBuilder(0)
		builder.AddRequest(request)
		gsmessage, err := builder.Build()
		require.NoError(t, err)
		require.NoError(t, gsData.GsNet2.SendMessage(ctx, host1.ID(), gsmessage))

		status := gsr.consumeResponses(ctx, t)
		require.True(t, gsmsg.IsTerminalFailureCode(status))
	})
}

func TestResponseHookWhenExtensionNotFound(t *testing.T) {
	// create network
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	gsData := testutil.NewGraphsyncTestingData(ctx, t, nil, nil)
	host1 := gsData.Host1 // initiator and data sender
	host2 := gsData.Host2 // data recipient, makes graphsync request for data
	voucher := testutil.FakeDTType{Data: "applesauce"}
	link := gsData.LoadUnixFSFile(t, false)

	// setup receiving peer to just record message coming in
	dtnet2 := network.NewFromLibp2pHost(host2)
	r := &receiver{
		messageReceived: make(chan receivedMessage),
	}
	dtnet2.SetDelegate(r)

	gsr := &fakeGraphSyncReceiver{
		receivedMessages: make(chan receivedGraphSyncMessage),
	}
	gsData.GsNet2.SetDelegate(gsr)

	gs1 := gsData.SetupGraphsyncHost1()
	tp1 := tp.NewTransport(host1.ID(), gs1)
	dt1, err := NewDataTransfer(gsData.DtDs1, gsData.TempDir1, gsData.DtNet1, tp1, gsData.StoredCounter1)
	require.NoError(t, err)
	testutil.StartAndWaitForReady(ctx, t, dt1)
	t.Run("when it's not our extension, does not error and does not validate", func(t *testing.T) {
		//register a hook that validates the request so we don't fail in gs because the request
		//never gets processed
		validateHook := func(p peer.ID, req graphsync.RequestData, ha graphsync.IncomingRequestHookActions) {
			ha.ValidateRequest()
		}
		gs1.RegisterIncomingRequestHook(validateHook)

		_, err := dt1.OpenPushDataChannel(ctx, host2.ID(), &voucher, link.(cidlink.Link).Cid, gsData.AllSelector)
		require.NoError(t, err)

		select {
		case <-ctx.Done():
			t.Fatal("did not receive message sent")
		case <-r.messageReceived:
		}

		request := gsmsg.NewRequest(graphsync.RequestID(rand.Int31()), link.(cidlink.Link).Cid, gsData.AllSelector, graphsync.Priority(rand.Int31()))
		builder := gsmsg.NewBuilder(0)
		builder.AddRequest(request)
		gsmessage, err := builder.Build()
		require.NoError(t, err)
		require.NoError(t, gsData.GsNet2.SendMessage(ctx, host1.ID(), gsmessage))

		status := gsr.consumeResponses(ctx, t)
		assert.False(t, gsmsg.IsTerminalFailureCode(status))
	})
}

func TestRespondingToPullGraphsyncRequests(t *testing.T) {
	//create network
	ctx := context.Background()
	testCases := map[string]struct {
		test func(*testing.T, *testutil.GraphsyncTestingData, datatransfer.Transport, ipld.Link, datatransfer.TransferID, *fakeGraphSyncReceiver)
	}{
		"When a pull request is initiated and validated": {
			test: func(t *testing.T, gsData *testutil.GraphsyncTestingData, tp2 datatransfer.Transport, link ipld.Link, id datatransfer.TransferID, gsr *fakeGraphSyncReceiver) {
				sv := testutil.NewStubbedValidator()
				sv.ExpectSuccessPull()

				dt1, err := NewDataTransfer(gsData.DtDs2, gsData.TempDir2, gsData.DtNet2, tp2, gsData.StoredCounter2)
				require.NoError(t, err)
				testutil.StartAndWaitForReady(ctx, t, dt1)
				require.NoError(t, dt1.RegisterVoucherType(&testutil.FakeDTType{}, sv))

				voucher := testutil.NewFakeDTType()
				request, err := message.NewRequest(id, false, true, voucher.Type(), voucher, testutil.GenerateCids(1)[0], gsData.AllSelector)
				require.NoError(t, err)
				buf := new(bytes.Buffer)
				err = request.ToNet(buf)
				require.NoError(t, err)
				extData := buf.Bytes()

				gsRequest := gsmsg.NewRequest(graphsync.RequestID(rand.Int31()), link.(cidlink.Link).Cid, gsData.AllSelector, graphsync.Priority(rand.Int31()), graphsync.ExtensionData{
					Name: extension.ExtensionDataTransfer1_1,
					Data: extData,
				})

				// initiator requests data over graphsync network
				builder := gsmsg.NewBuilder(0)
				builder.AddRequest(gsRequest)
				gsmessage, err := builder.Build()
				require.NoError(t, err)
				require.NoError(t, gsData.GsNet1.SendMessage(ctx, gsData.Host2.ID(), gsmessage))
				status := gsr.consumeResponses(ctx, t)
				require.False(t, gsmsg.IsTerminalFailureCode(status))
			},
		},
		"When request is initiated, but fails validation": {
			test: func(t *testing.T, gsData *testutil.GraphsyncTestingData, tp2 datatransfer.Transport, link ipld.Link, id datatransfer.TransferID, gsr *fakeGraphSyncReceiver) {
				sv := testutil.NewStubbedValidator()
				sv.ExpectErrorPull()
				dt1, err := NewDataTransfer(gsData.DtDs2, gsData.TempDir2, gsData.DtNet2, tp2, gsData.StoredCounter2)
				require.NoError(t, err)
				testutil.StartAndWaitForReady(ctx, t, dt1)
				require.NoError(t, dt1.RegisterVoucherType(&testutil.FakeDTType{}, sv))
				voucher := testutil.NewFakeDTType()
				dtRequest, err := message.NewRequest(id, false, true, voucher.Type(), voucher, testutil.GenerateCids(1)[0], gsData.AllSelector)
				require.NoError(t, err)

				buf := new(bytes.Buffer)
				err = dtRequest.ToNet(buf)
				require.NoError(t, err)
				extData := buf.Bytes()
				request := gsmsg.NewRequest(graphsync.RequestID(rand.Int31()), link.(cidlink.Link).Cid, gsData.AllSelector, graphsync.Priority(rand.Int31()), graphsync.ExtensionData{
					Name: extension.ExtensionDataTransfer1_1,
					Data: extData,
				})
				builder := gsmsg.NewBuilder(0)
				builder.AddRequest(request)
				gsmessage, err := builder.Build()
				require.NoError(t, err)

				// non-initiator requests data over graphsync network, but should not get it
				// because there was no previous request
				require.NoError(t, gsData.GsNet1.SendMessage(ctx, gsData.Host2.ID(), gsmessage))
				status := gsr.consumeResponses(ctx, t)
				require.True(t, gsmsg.IsTerminalFailureCode(status))
			},
		},
	}

	for testCase, data := range testCases {
		t.Run(testCase, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			gsData := testutil.NewGraphsyncTestingData(ctx, t, nil, nil)

			// setup receiving peer to just record message coming in
			gsr := &fakeGraphSyncReceiver{
				receivedMessages: make(chan receivedGraphSyncMessage),
			}
			gsData.GsNet1.SetDelegate(gsr)

			tp2 := gsData.SetupGSTransportHost2()

			link := gsData.LoadUnixFSFile(t, true)

			id := datatransfer.TransferID(rand.Int31())

			data.test(t, gsData, tp2, link, id, gsr)
		})
	}
}

type receivedMessage struct {
	message datatransfer.Message
	sender  peer.ID
}

// Receiver is an interface for receiving messages from the GraphSyncNetwork.
type receiver struct {
	messageReceived chan receivedMessage
}

func (r *receiver) ReceiveRequest(
	ctx context.Context,
	sender peer.ID,
	incoming datatransfer.Request) {

	select {
	case <-ctx.Done():
	case r.messageReceived <- receivedMessage{incoming, sender}:
	}
}

func (r *receiver) ReceiveResponse(
	ctx context.Context,
	sender peer.ID,
	incoming datatransfer.Response) {

	select {
	case <-ctx.Done():
	case r.messageReceived <- receivedMessage{incoming, sender}:
	}
}

func (r *receiver) ReceiveError(err error) {
}

func (r *receiver) ReceiveRestartExistingChannelRequest(ctx context.Context,
	sender peer.ID,
	incoming datatransfer.Request) {

}
