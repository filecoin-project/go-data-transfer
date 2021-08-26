package requestqueue

import (
	"context"
	"time"

	"github.com/ipfs/go-peertaskqueue"
	"github.com/ipfs/go-peertaskqueue/peertask"
	peer "github.com/libp2p/go-libp2p-core/peer"

	datatransfer "github.com/filecoin-project/go-data-transfer"
)

const (
	thawSpeed = time.Millisecond * 100
)

type Request interface {
	DataSender() peer.ID
	ChannelID() datatransfer.ChannelID
	Execute()
}

type RequestQueue struct {
	maxInProcessRequests uint64

	queue      *peertaskqueue.PeerTaskQueue
	workSignal chan struct{}
	ticker     *time.Ticker
}

type taskMerger struct {
}

func (tm taskMerger) HasNewInfo(task peertask.Task, existing []peertask.Task) bool {
	return true
}

func (tm taskMerger) Merge(task peertask.Task, existing *peertask.Task) {
}

func NewRequestQueue(maxInProcessRequests uint64) *RequestQueue {
	return &RequestQueue{
		maxInProcessRequests: maxInProcessRequests,
		queue:                peertaskqueue.New(peertaskqueue.TaskMerger(taskMerger{})),
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
			req := task.Data.(Request)
			req.Execute()
			orq.queue.TasksDone(pid, task)
		}
	}
}

func (orq *RequestQueue) AddRequest(ctx context.Context, request Request, priority int) {
	orq.queue.PushTasks(request.DataSender(), peertask.Task{Topic: request.ChannelID(), Priority: priority, Data: request, Work: 1})
	select {
	case orq.workSignal <- struct{}{}:
	default:
	}
}
