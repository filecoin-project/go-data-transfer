package impl

import (
	"context"

	"github.com/ipld/go-ipld-prime"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/ipld/go-ipld-prime/schema"
	"github.com/libp2p/go-libp2p-core/peer"
	"golang.org/x/xerrors"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/channels"
	"github.com/filecoin-project/go-data-transfer/message"
)

// ChannelDataTransferType identifies the type of a data transfer channel for the purposes of a restart
type ChannelDataTransferType int

const (
	// ManagerPeerCreatePull is the type of a channel wherein the manager peer created a Pull Data Transfer
	ManagerPeerCreatePull ChannelDataTransferType = iota

	// ManagerPeerCreatePush is the type of a channel wherein the manager peer created a Push Data Transfer
	ManagerPeerCreatePush

	// ManagerPeerReceivePull is the type of a channel wherein the manager peer received a Pull Data Transfer Request
	ManagerPeerReceivePull

	// ManagerPeerReceivePush is the type of a channel wherein the manager peer received a Push Data Transfer Request
	ManagerPeerReceivePush
)

func (m *manager) restartManagerPeerReceivePush(ctx context.Context, channel datatransfer.ChannelState) error {
	if err := m.validateRestartVoucher(channel, false); err != nil {
		return xerrors.Errorf("failed to restart channel, validation error: %w", err)
	}

	// send a libp2p message to the other peer asking to send a "restart push request"
	req := message.RestartExistingChannelRequest(channel.ChannelID())

	if err := m.dataTransferNetwork.SendMessage(ctx, channel.OtherPeer(), req); err != nil {
		return xerrors.Errorf("unable to send restart request: %w", err)
	}

	return nil
}

func (m *manager) restartManagerPeerReceivePull(ctx context.Context, channel datatransfer.ChannelState) error {
	if err := m.validateRestartVoucher(channel, true); err != nil {
		return xerrors.Errorf("failed to restart channel, validation error: %w", err)
	}

	req := message.RestartExistingChannelRequest(channel.ChannelID())

	// send a libp2p message to the other peer asking to send a "restart pull request"
	if err := m.dataTransferNetwork.SendMessage(ctx, channel.OtherPeer(), req); err != nil {
		return xerrors.Errorf("unable to send restart request: %w", err)
	}

	return nil
}

func (m *manager) validateRestartVoucher(channel datatransfer.ChannelState, isPull bool) error {
	// re-validate the original voucher received for safety
	chid := channel.ChannelID()

	// recreate the request that would have led to this pull channel being created for validation
	req, err := message.NewRequest(chid.ID, false, isPull, channel.VoucherType(), channel.Voucher(),
		channel.BaseCID(), channel.Selector())
	if err != nil {
		return err
	}

	// revalidate the voucher by reconstructing the request that would have led to the creation of this channel
	if _, _, err := m.validateVoucher(true, chid, channel.OtherPeer(), req, isPull, channel.BaseCID(), channel.Selector()); err != nil {
		return err
	}

	return nil
}

func (m *manager) openPushRestartChannel(ctx context.Context, channel datatransfer.ChannelState) error {
	selector := channel.Selector()
	voucher := channel.Voucher()
	baseCid := channel.BaseCID()
	requestTo := channel.OtherPeer()
	chid := channel.ChannelID()

	req, err := message.NewRequest(chid.ID, true, false, channel.VoucherType(), voucher, baseCid, selector)
	if err != nil {
		return err
	}

	processor, has := m.transportConfigurers.Processor(channel.VoucherType())
	if has {
		transportConfigurer := processor.(datatransfer.TransportConfigurer)
		transportConfigurer(chid, channel.VoucherType(), voucher, m.transport)
	}
	m.dataTransferNetwork.Protect(requestTo, chid.String())

	// Monitor the state of the connection for the channel
	monitoredChan := m.channelMonitor.AddPushChannel(chid)
	log.Infof("sending push restart channel to %s for channel %s", requestTo, chid)
	if err := m.dataTransferNetwork.SendMessage(ctx, requestTo, req); err != nil {
		// If push channel monitoring is enabled, shutdown the monitor as it
		// wasn't possible to start the data transfer
		if monitoredChan != nil {
			monitoredChan.Shutdown()
		}

		return xerrors.Errorf("Unable to send restart request: %w", err)
	}

	return nil
}

func (m *manager) openPullRestartChannel(ctx context.Context, channel datatransfer.ChannelState) error {
	selector := channel.Selector()
	voucher := channel.Voucher()
	baseCid := channel.BaseCID()
	requestTo := channel.OtherPeer()
	chid := channel.ChannelID()

	req, err := message.NewRequest(chid.ID, true, true, channel.VoucherType(), voucher, baseCid, selector)
	if err != nil {
		return err
	}

	processor, has := m.transportConfigurers.Processor(channel.VoucherType())
	if has {
		transportConfigurer := processor.(datatransfer.TransportConfigurer)
		transportConfigurer(chid, channel.VoucherType(), voucher, m.transport)
	}
	m.dataTransferNetwork.Protect(requestTo, chid.String())

	// Monitor the state of the connection for the channel
	monitoredChan := m.channelMonitor.AddPullChannel(chid)
	log.Infof("sending open channel to %s to restart channel %s", requestTo, chid)
	if err := m.transport.OpenChannel(ctx, requestTo, chid, cidlink.Link{Cid: baseCid}, selector, channel, req); err != nil {
		// If pull channel monitoring is enabled, shutdown the monitor as it
		// wasn't possible to start the data transfer
		if monitoredChan != nil {
			monitoredChan.Shutdown()
		}

		return xerrors.Errorf("Unable to send open channel restart request: %w", err)
	}

	return nil
}

func (m *manager) validateRestartRequest(ctx context.Context, otherPeer peer.ID, chid datatransfer.ChannelID, req datatransfer.Request) error {
	// channel should exist
	channel, err := m.channels.GetByID(ctx, chid)
	if err != nil {
		return err
	}

	// channel is not terminated
	if channels.IsChannelTerminated(channel.Status()) {
		return xerrors.New("channel is already terminated")
	}

	// channel initator should be the sender peer
	if channel.ChannelID().Initiator != otherPeer {
		return xerrors.New("other peer is not the initiator of the channel")
	}

	// channel and request baseCid should match
	if req.BaseCid() != channel.BaseCID() {
		return xerrors.New("base cid does not match")
	}

	reqVoucher, err := req.Voucher()
	if err != nil {
		return err
	}
	if tn, ok := reqVoucher.(schema.TypedNode); ok {
		reqVoucher = tn.Representation()
	}

	// voucher types should match
	if req.VoucherType() != channel.VoucherType() {
		return xerrors.New("channel and request voucher types do not match")
	}

	vouch := channel.Voucher()
	if tn, ok := vouch.(schema.TypedNode); ok {
		vouch = tn.Representation()
	}
	if !ipld.DeepEqual(reqVoucher, channel.Voucher()) {
		return xerrors.New("channel and request vouchers do not match")
	}

	return nil
}
