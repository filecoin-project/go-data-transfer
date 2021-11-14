package bitswap

import (
	"context"
	"encoding/binary"
	"errors"
	datatransfer "github.com/filecoin-project/go-data-transfer"
	bsclient "github.com/ipfs/go-bitswap/client"
	bsnet "github.com/ipfs/go-bitswap/network"
	"github.com/ipfs/go-cid"
	ipldformat "github.com/ipfs/go-ipld-format"
	"github.com/ipfs/go-merkledag"
	"github.com/ipld/go-ipld-prime"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/libp2p/go-libp2p-core/peer"
	"sync"
	"sync/atomic"
)

type Transport struct {
	events datatransfer.EventsHandler

	chlk sync.RWMutex
	channels map[datatransfer.ChannelID]*dtChan

	bn bsnet.BitSwapNetwork
}

var _ datatransfer.Transport = (*Transport)(nil)

type dtChan struct {
	sender peer.ID
	chanID datatransfer.ChannelID
	root ipld.Link
	selector ipld.Node

	ctx context.Context
	close func()

	t *Transport
}

func (d *dtChan) start(la *bsclient.LightAuth) error {
	rl := d.root.(cidlink.Link)
	cs := cid.NewSet()
	var index int64 = -1
	err := merkledag.Walk(d.ctx, func(ctx context.Context, c cid.Cid) ([]*ipldformat.Link, error) {
		blk, err := la.GetBlock(ctx, c)
		if err != nil {
			return nil, err
		}
		nd ,err := ipldformat.Decode(blk)
		if err != nil {
			return nil, err
		}

		currIndex := atomic.AddInt64(&index, 1)
		if err == nil {
			e := d.t.events.OnDataReceived(d.chanID, cidlink.Link{Cid: c}, uint64(len(blk.RawData())), currIndex)
			if e == nil {
			} else if errors.Is(e, datatransfer.ErrPause) {
				panic("err not implemented")
			} else {
				return nil, err
			}
		} else {
			return nil, err
		}

		return nd.Links(), nil
	}, rl.Cid, cs.Visit, merkledag.Concurrent())

	err = d.t.events.OnChannelCompleted(d.chanID, err)

	return err
}

func (d *dtChan) Close() error {
	d.close()
	return nil
}

type bsEvtHandler struct {

}

func (b *bsEvtHandler) OnChannelOpened(chid datatransfer.ChannelID) error {
	panic("implement me")
}

func (b *bsEvtHandler) OnResponseReceived(chid datatransfer.ChannelID, msg datatransfer.Response) error {
	panic("implement me")
}

func (b *bsEvtHandler) OnDataReceived(chid datatransfer.ChannelID, link ipld.Link, size uint64, index int64) error {
	panic("implement me")
}

func (b *bsEvtHandler) OnDataQueued(chid datatransfer.ChannelID, link ipld.Link, size uint64) (datatransfer.Message, error) {
	panic("implement me")
}

func (b *bsEvtHandler) OnDataSent(chid datatransfer.ChannelID, link ipld.Link, size uint64) error {
	panic("implement me")
}

func (b *bsEvtHandler) OnTransferQueued(chid datatransfer.ChannelID) {
	panic("implement me")
}

func (b *bsEvtHandler) OnRequestReceived(chid datatransfer.ChannelID, msg datatransfer.Request) (datatransfer.Response, error) {
	panic("implement me")
}

func (b *bsEvtHandler) OnChannelCompleted(chid datatransfer.ChannelID, err error) error {
	return nil
}

func (b *bsEvtHandler) OnRequestCancelled(chid datatransfer.ChannelID, err error) error {
	panic("implement me")
}

func (b *bsEvtHandler) OnRequestDisconnected(chid datatransfer.ChannelID, err error) error {
	panic("implement me")
}

func (b *bsEvtHandler) OnSendDataError(chid datatransfer.ChannelID, err error) error {
	panic("implement me")
}

func (b *bsEvtHandler) OnReceiveDataError(chid datatransfer.ChannelID, err error) error {
	panic("implement me")
}

var _ datatransfer.EventsHandler = (*bsEvtHandler)(nil)

func New(bn bsnet.BitSwapNetwork) (*Transport, error) {
	t := &Transport{
		channels: make(map[datatransfer.ChannelID]*dtChan),
		bn: bn,
	}
	return t, nil
}

func (t *Transport) OpenChannel(ctx context.Context, dataSender peer.ID, channelID datatransfer.ChannelID, root ipld.Link, stor ipld.Node, channel datatransfer.ChannelState, msg datatransfer.Message) error {
	if t.events == nil {
		return datatransfer.ErrHandlerNotSet
	}

	t.chlk.Lock()
	defer t.chlk.Unlock()
	if _, found := t.channels[channelID]; found {
		panic("this channel already exists")
	}

	ctx, cancel := context.WithCancel(context.Background())

	c := &dtChan{
		sender:   dataSender,
		chanID:   channelID,
		root:     root,
		selector: stor,
		ctx : ctx,
		close : cancel,
		t : t,
	}
	t.channels[channelID] = c

	token := make([]byte, 9)
	binary.PutUvarint(token, uint64(channelID.ID))
	la, err := bsclient.NewLightAuth(t.bn, dataSender, token)
	if err != nil {
		return err
	}

	go c.start(la)
	return nil
}

func (t *Transport) CloseChannel(ctx context.Context, chid datatransfer.ChannelID) error {
	panic("not implemented")
	if t.events == nil {
		return datatransfer.ErrHandlerNotSet
	}
	t.chlk.Lock()
	defer t.chlk.Unlock()
	c, ok := t.channels[chid]
	if !ok {
		return datatransfer.ErrChannelNotFound
	}
	if err := c.Close(); err != nil {
		return err
	}
	delete(t.channels, chid)
	return nil
}

func (t *Transport) SetEventHandler(events datatransfer.EventsHandler) error {
	if t.events != nil {
		return datatransfer.ErrHandlerAlreadySet
	}
	t.events = events
	return nil
}

func (t *Transport) CleanupChannel(chid datatransfer.ChannelID) {
	t.chlk.Lock()
	defer t.chlk.Unlock()
	delete(t.channels, chid)
}

func (t *Transport) Shutdown(ctx context.Context) error {
	t.chlk.Lock()
	defer t.chlk.Unlock()
	t.channels = make(map[datatransfer.ChannelID]*dtChan)
	return nil
}
