package impl

import (
	"context"
	"sync"
	"time"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/channels"
)

type monitorAPI interface {
	SubscribeToEvents(subscriber datatransfer.Subscriber) datatransfer.Unsubscribe
	RestartDataTransferChannel(ctx context.Context, chid datatransfer.ChannelID) error
	CloseDataTransferChannel(ctx context.Context, chid datatransfer.ChannelID) error
}

// pushChannelMonitor watches the data-rate for push channels, and restarts
// a channel if the data-rate falls too low
type pushChannelMonitor struct {
	ctx  context.Context
	stop context.CancelFunc
	mgr  monitorAPI
	cfg  *pushMonitorConfig

	lk       sync.RWMutex
	channels map[*monitoredChannel]struct{}
}

type pushMonitorConfig struct {
	Interval       time.Duration
	MinBytesSent   uint64
	RestartBackoff time.Duration
}

func newPushChannelMonitor(mgr monitorAPI) *pushChannelMonitor {
	ctx, cancel := context.WithCancel(context.Background())
	return &pushChannelMonitor{
		ctx:      ctx,
		stop:     cancel,
		mgr:      mgr,
		channels: make(map[*monitoredChannel]struct{}),
	}
}

func (m *pushChannelMonitor) setConfig(cfg *pushMonitorConfig) {
	m.lk.Lock()
	defer m.lk.Unlock()

	m.cfg = cfg
}

// add a channel to the push channel monitor
func (m *pushChannelMonitor) add(chid datatransfer.ChannelID) *monitoredChannel {
	m.lk.Lock()
	defer m.lk.Unlock()

	mpc := newMonitoredChannel(m.mgr, chid, m.cfg, m.onMonitoredChannelShutdown)
	m.channels[mpc] = struct{}{}
	return mpc
}

func (m *pushChannelMonitor) shutdown() {
	m.stop()
}

// onShutdown shuts down all monitored channels
func (m *pushChannelMonitor) onShutdown() {
	m.lk.RLock()
	defer m.lk.RUnlock()

	for ch := range m.channels {
		ch.shutdown()
	}
}

// onMonitoredChannelShutdown is called when a monitored channel shuts down
func (m *pushChannelMonitor) onMonitoredChannelShutdown(mpc *monitoredChannel) {
	m.lk.Lock()
	defer m.lk.Unlock()

	delete(m.channels, mpc)
}

func (m *pushChannelMonitor) enabled() bool {
	return m.cfg != nil && m.cfg.Interval > 0
}

func (m *pushChannelMonitor) start() {
	go m.run()
}

func (m *pushChannelMonitor) run() {
	defer m.onShutdown()

	// Check the data-rate of all monitored channels on every tick
	var tickerChan <-chan time.Time
	if m.enabled() {
		ticker := time.NewTicker(m.cfg.Interval)
		tickerChan = ticker.C
		defer ticker.Stop()
	}

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-tickerChan:
			m.checkDataRate()
		}
	}
}

// check data rate for all monitored channels
func (m *pushChannelMonitor) checkDataRate() {
	m.lk.RLock()
	defer m.lk.RUnlock()

	for ch := range m.channels {
		ch.checkDataRate()
	}
}

// monitoredChannel keeps track of the data-rate for a push channel, and
// restarts the channel if the rate falls below the minimum allowed
type monitoredChannel struct {
	ctx        context.Context
	cancel     context.CancelFunc
	mgr        monitorAPI
	chid       datatransfer.ChannelID
	cfg        *pushMonitorConfig
	unsub      datatransfer.Unsubscribe
	onShutdown func(*monitoredChannel)

	statsLk  sync.RWMutex
	queued   uint64
	sent     uint64
	lastSent uint64

	restartLk  sync.RWMutex
	restarting bool
}

func newMonitoredChannel(
	mgr monitorAPI,
	chid datatransfer.ChannelID,
	cfg *pushMonitorConfig,
	onShutdown func(*monitoredChannel),
) *monitoredChannel {
	ctx, cancel := context.WithCancel(context.Background())
	mpc := &monitoredChannel{
		ctx:        ctx,
		cancel:     cancel,
		mgr:        mgr,
		chid:       chid,
		cfg:        cfg,
		onShutdown: onShutdown,
	}
	mpc.start()
	return mpc
}

// Cancel the context and unsubscribe from events
func (mc *monitoredChannel) shutdown() {
	mc.cancel()
	mc.unsub()
	go mc.onShutdown(mc)
}

func (mc *monitoredChannel) start() {
	mc.unsub = mc.mgr.SubscribeToEvents(func(event datatransfer.Event, channelState datatransfer.ChannelState) {
		if channelState.ChannelID() != mc.chid {
			return
		}

		mc.statsLk.Lock()
		defer mc.statsLk.Unlock()

		// Once the channel completes, shut down the monitor
		state := channelState.Status()
		if channels.IsChannelCleaningUp(state) || channels.IsChannelTerminated(state) {
			go mc.shutdown()
			return
		}

		switch event.Code {
		case datatransfer.Error:
			// If there's an error, attempt to restart the channel
			go mc.restartChannel()
		case datatransfer.DataQueued:
			// Keep track of the amount of data queued
			mc.queued = channelState.Queued()
		case datatransfer.DataSent:
			// Keep track of the amount of data sent
			mc.sent = channelState.Sent()
		}
	})
}

// check if the amount of data sent in the interval was too low, and if so
// restart the channel
func (mc *monitoredChannel) checkDataRate() {
	mc.statsLk.Lock()
	defer mc.statsLk.Unlock()

	sentInInterval := mc.sent - mc.lastSent
	mc.lastSent = mc.sent
	pending := mc.queued - mc.sent
	if pending > 0 && sentInInterval < mc.cfg.MinBytesSent {
		go mc.restartChannel()
	}
}

func (mc *monitoredChannel) restartChannel() {
	// Check if the channel is already being restarted
	mc.restartLk.Lock()
	alreadyRestarting := mc.restarting
	if !alreadyRestarting {
		mc.restarting = true
	}
	mc.restartLk.Unlock()

	if alreadyRestarting {
		return
	}

	defer func() {
		// Backoff a little time after a restart before attempting another
		select {
		case <-time.After(mc.cfg.RestartBackoff):
		case <-mc.ctx.Done():
		}

		mc.restartLk.Lock()
		mc.restarting = false
		mc.restartLk.Unlock()
	}()

	// Send a restart message for the channel
	log.Infof("Sending restart message for push data-channel %s", mc.chid)
	err := mc.mgr.RestartDataTransferChannel(mc.ctx, mc.chid)
	if err != nil {
		log.Warnf("closing channel after failing to send restart message for push data-channel %s: %s", mc.chid, err)

		// If it wasn't possible to restart the channel, close the channel
		// and shut down the monitor
		defer mc.shutdown()

		err := mc.mgr.CloseDataTransferChannel(mc.ctx, mc.chid)
		if err != nil {
			log.Errorf("error closing data transfer channel %s: %w", mc.chid, err)
		}
	}
}
