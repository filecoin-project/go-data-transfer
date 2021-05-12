package impl

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"os"
	"sort"
	"testing"
	"time"

	cborutil "github.com/filecoin-project/go-cbor-util"
	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/testutil"
	"github.com/ipfs/go-cid"
	chunker "github.com/ipfs/go-ipfs-chunker"
	files "github.com/ipfs/go-ipfs-files"
	ipldformat "github.com/ipfs/go-ipld-format"
	"github.com/ipfs/go-unixfs/importer/balanced"
	ihelper "github.com/ipfs/go-unixfs/importer/helpers"
	"github.com/ipld/go-ipld-prime"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	basicnode "github.com/ipld/go-ipld-prime/node/basic"
	"github.com/ipld/go-ipld-prime/traversal/selector"
	"github.com/ipld/go-ipld-prime/traversal/selector/builder"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/mux"
	"github.com/libp2p/go-libp2p-core/network"
	peer "github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-core/protocol"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"
	cbg "github.com/whyrusleeping/cbor-gen"
	"golang.org/x/xerrors"
)

func TestMultiRequestStreams(t *testing.T) {
	for i := 0; i < 1; i++ {
		t.Run(fmt.Sprintf("Run %d", i), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()

			mn := mocknet.New(ctx)
			gsData := testutil.NewGraphsyncTestingData(ctx, t, nil, nil, mn)

			root, origBytes := LoadRandomData(ctx, t, gsData.DagService1)
			rootCid := root.(cidlink.Link).Cid
			tp1 := gsData.SetupGSTransportHost1()

			dt1, err := NewDataTransfer(gsData.DtDs1, gsData.TempDir1, gsData.DtNet1, tp1)
			require.NoError(t, err)
			sv := testutil.NewStubbedValidator()
			require.NoError(t, dt1.RegisterVoucherType(&Request{}, sv))

			testutil.StartAndWaitForReady(ctx, t, dt1)

			hn := &harness{
				h:   gsData.Host1,
				dt:  dt1,
				net: NewNetwork(gsData.Host1),
			}
			hn.net.SetDelegate(&handler{dt1})

			var testData []*testutil.GraphsyncTestingData
			receivers := make(map[peer.ID]*harness)
			verifiers := make(map[peer.ID]func())

			for i := 0; i < 7; i++ {
				gsData := testutil.NewGraphsyncTestingData(ctx, t, nil, nil, mn)
				gsData.OrigBytes = origBytes
				tp1 := gsData.SetupGSTransportHost1()
				tp2 := gsData.SetupGSTransportHost2()
				dt1, err := NewDataTransfer(gsData.DtDs1, gsData.TempDir1, gsData.DtNet1, tp1)
				require.NoError(t, err)
				dt1.RegisterVoucherType(&Request{}, nil)
				testutil.StartAndWaitForReady(ctx, t, dt1)

				dt2, err := NewDataTransfer(gsData.DtDs2, gsData.TempDir2, gsData.DtNet2, tp2)
				require.NoError(t, err)
				dt2.RegisterVoucherType(&Request{}, nil)
				testutil.StartAndWaitForReady(ctx, t, dt2)

				hn1 := &harness{
					h:   gsData.Host1,
					dt:  dt1,
					net: NewNetwork(gsData.Host1),
				}
				hn1.net.SetDelegate(&handler{dt1})
				receivers[gsData.Host1.ID()] = hn1
				verifiers[gsData.Host1.ID()] = func() {
					gsData.VerifyFileTransferred(t, root, false)
				}
				hn2 := &harness{
					h:   gsData.Host2,
					dt:  dt2,
					net: NewNetwork(gsData.Host2),
				}
				hn1.net.SetDelegate(&handler{dt2})
				receivers[gsData.Host2.ID()] = hn2
				testData = append(testData, gsData)

				// Channel that will be closed when all data has been received
				allDataReceived := make(chan struct{})
				dt2.SubscribeToEvents(func(event datatransfer.Event, channelState datatransfer.ChannelState) {
					if channelState.Status() == datatransfer.Completed {
						close(allDataReceived)
					}
				})
				verifiers[gsData.Host2.ID()] = func() {
					// Wait for all data to be received
					select {
					case <-allDataReceived:
					case <-ctx.Done():
						t.Fatal("timed out waiting for all data to be received")
						//default:
						//	t.Fatal("verify called before all data received")
					}
					// Verify all data was received correctly
					gsData.VerifyFileTransferred(t, root, true)
				}
			}

			err = mn.ConnectAllButSelf()
			require.NoError(t, err)

			res, err := hn.Dispatch(Request{rootCid, uint64(len(origBytes))})
			defer res.Close()
			require.NoError(t, err)

			var recs []PRecord
			for len(recs) < 7 {
				rec, err := res.Next(ctx)
				require.NoError(t, err)
				recs = append(recs, rec)
			}

			for _, r := range recs {
				verifiers[r.Provider]()
			}

		})
	}
}

// RequestProtocol labels the stream protocol
const RequestProtocol = protocol.ID("/rq-pl")

// Request describes the content to pull
type Request struct {
	PayloadCID cid.Cid
	Size       uint64
}

// Type defines AddRequest as a datatransfer voucher for pulling the data from the request
func (Request) Type() datatransfer.TypeIdentifier {
	return "DispatchRequestVoucher"
}

// PRecord is a provider <> cid mapping for recording who is storing what content
type PRecord struct {
	Provider   peer.ID
	PayloadCID cid.Cid
}

// Response is an async collection of confirmations from data transfers to cache providers
type Response struct {
	recordChan chan PRecord
	unsub      datatransfer.Unsubscribe
}

// Next returns the next record from a new cache
func (r *Response) Next(ctx context.Context) (PRecord, error) {
	select {
	case r := <-r.recordChan:
		return r, nil
	case <-ctx.Done():
		return PRecord{}, ctx.Err()
	}
}

// Close stops listening for cache confirmations
func (r *Response) Close() {
	r.unsub()
	close(r.recordChan)
}

// Network handles all the different messaging protocols
// related to content supply
type Network struct {
	host     host.Host
	receiver StreamReceiver
}

// NewNetwork creates a new Network instance
func NewNetwork(h host.Host) *Network {
	sn := &Network{
		host: h,
	}
	return sn
}

// NewRequestStream to send AddRequest messages to
func (n *Network) NewRequestStream(dest peer.ID) (RequestStreamer, error) {
	s, err := n.host.NewStream(context.Background(), dest, RequestProtocol)
	if err != nil {
		return nil, err
	}
	buffered := bufio.NewReaderSize(s, 16)
	return &requestStream{p: dest, rw: s, buffered: buffered}, nil
}

// SetDelegate assigns a handler for all the protocols
func (n *Network) SetDelegate(sr StreamReceiver) {
	n.receiver = sr
	n.host.SetStreamHandler(RequestProtocol, n.handleStream)
}

func (n *Network) handleStream(s network.Stream) {
	if n.receiver == nil {
		fmt.Printf("no receiver set")
		s.Reset()
		return
	}
	remotePID := s.Conn().RemotePeer()
	buffered := bufio.NewReaderSize(s, 16)
	ns := &requestStream{remotePID, s, buffered}
	n.receiver.HandleRequest(ns)
}

// StreamReceiver will read the stream and do something in response
type StreamReceiver interface {
	HandleRequest(RequestStreamer)
}

// RequestStreamer reads AddRequest structs from a muxed stream
type RequestStreamer interface {
	ReadRequest() (Request, error)
	WriteRequest(Request) error
	OtherPeer() peer.ID
	Close() error
}

type requestStream struct {
	p        peer.ID
	rw       mux.MuxedStream
	buffered *bufio.Reader
}

func (a *requestStream) ReadRequest() (Request, error) {
	var m Request
	if err := m.UnmarshalCBOR(a.buffered); err != nil {
		return Request{}, err
	}
	return m, nil
}

func (a *requestStream) WriteRequest(m Request) error {
	return cborutil.WriteCborRPC(a.rw, &m)
}

func (s *requestStream) Close() error {
	return s.rw.Close()
}

func (s *requestStream) OtherPeer() peer.ID {
	return s.p
}

type handler struct {
	dt datatransfer.Manager
}

func AllSelector() ipld.Node {
	ssb := builder.NewSelectorSpecBuilder(basicnode.Prototype.Any)
	return ssb.ExploreRecursive(selector.RecursionLimitNone(),
		ssb.ExploreAll(ssb.ExploreRecursiveEdge())).Node()
}

// HandleRequest pulls the blocks from the peer upon receiving the request
func (h *handler) HandleRequest(stream RequestStreamer) {
	defer stream.Close()

	req, err := stream.ReadRequest()
	if err != nil {
		return
	}

	_, err = h.dt.OpenPullDataChannel(context.TODO(), stream.OtherPeer(), &req, req.PayloadCID, AllSelector())
	if err != nil {
		return
	}
}

type harness struct {
	h   host.Host
	dt  datatransfer.Manager
	net *Network
}

// Dispatch requests to the network until we have propagated the content to enough peers
func (h *harness) Dispatch(r Request) (*Response, error) {
	res := &Response{
		recordChan: make(chan PRecord),
	}

	// listen for datatransfer events to identify the peers who pulled the content
	res.unsub = h.dt.SubscribeToEvents(func(event datatransfer.Event, chState datatransfer.ChannelState) {
		if chState.Status() == datatransfer.Completed {
			root := chState.BaseCID()
			if root != r.PayloadCID {
				return
			}
			// The recipient is the provider who received our content
			rec := chState.Recipient()
			res.recordChan <- PRecord{
				Provider:   rec,
				PayloadCID: root,
			}
		}
	})

	// Select the providers we want to send to
	providers, err := h.selectProviders()
	if err != nil {
		return res, err
	}
	h.sendAllRequests(r, providers)
	return res, nil
}

func (h *harness) selectProviders() ([]peer.ID, error) {
	var peers []peer.ID
	// Get the current connected peers
	for _, pconn := range h.h.Network().Conns() {
		pid := pconn.RemotePeer()
		// Make sure we don't add ourselves
		if pid != h.h.ID() {
			peers = append(peers, pid)
		}
	}
	return peers, nil
}

func (h *harness) sendAllRequests(r Request, peers []peer.ID) {
	for _, p := range peers {
		stream, err := h.net.NewRequestStream(p)
		if err != nil {
			continue
		}
		err = stream.WriteRequest(r)
		stream.Close()
		if err != nil {
			continue
		}
	}
}

var _ = xerrors.Errorf
var _ = cid.Undef
var _ = sort.Sort

var lengthBufRequest = []byte{130}

func (t *Request) MarshalCBOR(w io.Writer) error {
	if t == nil {
		_, err := w.Write(cbg.CborNull)
		return err
	}
	if _, err := w.Write(lengthBufRequest); err != nil {
		return err
	}

	scratch := make([]byte, 9)

	// t.PayloadCID (cid.Cid) (struct)

	if err := cbg.WriteCidBuf(scratch, w, t.PayloadCID); err != nil {
		return xerrors.Errorf("failed to write cid field t.PayloadCID: %w", err)
	}

	// t.Size (uint64) (uint64)

	if err := cbg.WriteMajorTypeHeaderBuf(scratch, w, cbg.MajUnsignedInt, uint64(t.Size)); err != nil {
		return err
	}

	return nil
}

func (t *Request) UnmarshalCBOR(r io.Reader) error {
	*t = Request{}

	br := cbg.GetPeeker(r)
	scratch := make([]byte, 8)

	maj, extra, err := cbg.CborReadHeaderBuf(br, scratch)
	if err != nil {
		return err
	}
	if maj != cbg.MajArray {
		return fmt.Errorf("cbor input should be of type array")
	}

	if extra != 2 {
		return fmt.Errorf("cbor input had wrong number of fields")
	}

	// t.PayloadCID (cid.Cid) (struct)

	{

		c, err := cbg.ReadCid(br)
		if err != nil {
			return xerrors.Errorf("failed to read cid field t.PayloadCID: %w", err)
		}

		t.PayloadCID = c

	}
	// t.Size (uint64) (uint64)

	{

		maj, extra, err = cbg.CborReadHeaderBuf(br, scratch)
		if err != nil {
			return err
		}
		if maj != cbg.MajUnsignedInt {
			return fmt.Errorf("wrong type for uint64 field")
		}
		t.Size = uint64(extra)

	}

	return nil
}

const unixfsChunkSize uint64 = 1 << 10
const unixfsLinksPerLevel = 1024

func LoadRandomData(ctx context.Context, t *testing.T, dagService ipldformat.DAGService) (ipld.Link, []byte) {
	tf, err := ioutil.TempFile("", "data")
	//tf, err := os.CreateTemp("", "data")
	require.NoError(t, err)
	t.Cleanup(func() {
		os.Remove(tf.Name())
	})

	data := make([]byte, 256000)
	rand.New(rand.NewSource(time.Now().UnixNano())).Read(data)
	_, err = tf.Write(data)
	require.NoError(t, err)

	f, err := os.Open(tf.Name())
	require.NoError(t, err)

	var buf bytes.Buffer
	tr := io.TeeReader(f, &buf)
	file := files.NewReaderFile(tr)

	// import to UnixFS
	bufferedDS := ipldformat.NewBufferedDAG(ctx, dagService)

	params := ihelper.DagBuilderParams{
		Maxlinks:   unixfsLinksPerLevel,
		RawLeaves:  true,
		CidBuilder: nil,
		Dagserv:    bufferedDS,
	}

	db, err := params.New(chunker.NewSizeSplitter(file, int64(unixfsChunkSize)))
	require.NoError(t, err)

	nd, err := balanced.Layout(db)
	require.NoError(t, err)

	err = bufferedDS.Commit()
	require.NoError(t, err)

	// save the original files bytes
	return cidlink.Link{Cid: nd.Cid()}, buf.Bytes()
}
