package requestqueue

import (
	"context"
	"math"
	"time"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-graphsync"
	"github.com/ipfs/go-peertaskqueue"
	"github.com/ipfs/go-peertaskqueue/peertask"
	ipld "github.com/ipld/go-ipld-prime"
	peer "github.com/libp2p/go-libp2p-core/peer"
)

const (
	thawSpeed = time.Millisecond * 100
)

type RunDeferredRequestFn func(ctx context.Context, chid datatransfer.ChannelID, dataSender peer.ID, root ipld.Link, stor ipld.Node, doNotSendCids []cid.Cid, exts []graphsync.ExtensionData)
type RequestQueue struct {
	runDeferredRequest   RunDeferredRequestFn
	maxInProcessRequests uint64

	queue      *peertaskqueue.PeerTaskQueue
	workSignal chan struct{}
	ticker     *time.Ticker
}

func NewRequestQueue(runDeferredRequest RunDeferredRequestFn, maxInProcessRequests uint64) *RequestQueue {
	return &RequestQueue{
		maxInProcessRequests: maxInProcessRequests,
		runDeferredRequest:   runDeferredRequest,
		queue:                peertaskqueue.New(),
		workSignal:           make(chan struct{}, 1),
		ticker:               time.NewTicker(thawSpeed),
	}
}

func (orq *RequestQueue) Start(ctx context.Context) {
	for i := uint64(0); i < orq.maxInProcessRequests; i++ {
		go orq.processRequestWorker(ctx)
	}
}

func (orq *RequestQueue) processRequestWorker(ctx context.Context) {
	const targetWork = 1
	for {
		pid, tasks, _ := orq.queue.PopTasks(targetWork)
		for len(tasks) == 0 {
			select {
			case <-ctx.Done():
				return
			case <-orq.workSignal:
				pid, tasks, _ = orq.queue.PopTasks(targetWork)
			case <-orq.ticker.C:
				orq.queue.ThawRound()
				pid, tasks, _ = orq.queue.PopTasks(targetWork)
			}
		}
		for _, task := range tasks {
			chid := task.Topic.(datatransfer.ChannelID)
			ri := task.Data.(requestInfo)
			orq.runDeferredRequest(ri.ctx, chid, pid, ri.root, ri.stor, ri.doNotSendCids, ri.exts)
			orq.queue.TasksDone(pid, task)
		}
	}
}

type requestInfo struct {
	ctx           context.Context
	root          ipld.Link
	stor          ipld.Node
	doNotSendCids []cid.Cid
	exts          []graphsync.ExtensionData
}

func (orq *RequestQueue) AddRequest(ctx context.Context, chid datatransfer.ChannelID, dataSender peer.ID, root ipld.Link, stor ipld.Node, doNotSendCids []cid.Cid, exts []graphsync.ExtensionData) {
	orq.queue.PushTasks(dataSender, peertask.Task{Topic: chid, Priority: math.MaxInt32, Data: requestInfo{ctx, root, stor, doNotSendCids, exts}, Work: 1})
	select {
	case orq.workSignal <- struct{}{}:
	default:
	}
}
