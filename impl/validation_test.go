package impl

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"testing"
	"time"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/encoding"
	"github.com/filecoin-project/go-data-transfer/testutil"
	chunker "github.com/ipfs/go-ipfs-chunker"
	files "github.com/ipfs/go-ipfs-files"
	ipldformat "github.com/ipfs/go-ipld-format"
	"github.com/ipfs/go-unixfs/importer/balanced"
	ihelper "github.com/ipfs/go-unixfs/importer/helpers"
	"github.com/ipld/go-ipld-prime"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/stretchr/testify/require"
)

const unixfsChunkSize uint64 = 1 << 10
const unixfsLinksPerLevel = 1024

func TestRevalidateRetrievalFlow(t *testing.T) {
	pausePoints := []uint64{1000, 3000, 6000, 10000, 15000}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	gsData := testutil.NewGraphsyncTestingData(ctx, t, nil, nil)
	host1 := gsData.Host1 // initiator, data sender

	root, origBytes := LoadRandomData(ctx, t, gsData.DagService1)
	gsData.OrigBytes = origBytes
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
	clientGotResponse := make(chan struct{}, 1)

	// The response voucher is the voucher returned by the request validator
	respVoucher := testutil.NewFakeDTType()
	encodedRVR, err := encoding.Encode(respVoucher)
	require.NoError(t, err)

	finalVoucherResult := testutil.NewFakeDTType()
	encodedFVR, err := encoding.Encode(finalVoucherResult)
	require.NoError(t, err)

	dt2.SubscribeToEvents(func(event datatransfer.Event, channelState datatransfer.ChannelState) {
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
			if bytes.Equal(encodedLVR, encodedRVR) {
				clientGotResponse <- struct{}{}
			}
		}
		if event.Code == datatransfer.DataReceived &&
			clientPausePoint < len(pausePoints) &&
			channelState.Received() > pausePoints[clientPausePoint] {
			_ = dt2.SendVoucher(ctx, chid, testutil.NewFakeDTType())
			clientPausePoint++
		}

		if channelState.Status() == datatransfer.Completed {
			clientFinished <- struct{}{}
		}
	})

	providerFinished := make(chan struct{}, 1)
	dt1.SubscribeToEvents(func(event datatransfer.Event, channelState datatransfer.ChannelState) {
		if event.Code == datatransfer.Error {
			errChan <- struct{}{}
		}
		if channelState.Status() == datatransfer.Completed {
			providerFinished <- struct{}{}
		}
	})

	voucher := testutil.FakeDTType{Data: "applesauce"}
	sv := testutil.NewStubbedValidator()
	require.NoError(t, dt1.RegisterVoucherType(&testutil.FakeDTType{}, sv))

	srv := &retrievalRevalidator{
		testutil.NewStubbedRevalidator(), 0, 0, pausePoints, finalVoucherResult,
	}
	srv.ExpectSuccessRevalidation()
	require.NoError(t, dt1.RegisterRevalidator(testutil.NewFakeDTType(), srv))

	// Register our response voucher
	require.NoError(t, dt2.RegisterVoucherResultType(respVoucher))
	// Stub in the validator to make sure we receive that voucher
	sv.StubResult(respVoucher)

	chid, err = dt2.OpenPullDataChannel(ctx, host1.ID(), &voucher, rootCid, gsData.AllSelector)
	require.NoError(t, err)

	for clientGotResponse != nil || providerFinished != nil || clientFinished != nil {
		select {
		case <-ctx.Done():
			t.Fatal("Did not complete successful data transfer")
		case <-clientGotResponse:
			clientGotResponse = nil
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
		fmt.Println("OnPullDataSent")
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

func LoadRandomData(ctx context.Context, t *testing.T, dagService ipldformat.DAGService) (ipld.Link, []byte) {
	tf, err := os.CreateTemp("", "data")
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
