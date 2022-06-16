package impl

import (
	"context"

	"github.com/ipld/go-ipld-prime"
	"github.com/libp2p/go-libp2p-core/peer"
	"golang.org/x/xerrors"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/channels"
	"github.com/filecoin-project/go-data-transfer/v2/message"
)

func (m *manager) restartManagerPeerReceive(ctx context.Context, channel datatransfer.ChannelState) error {

	if !m.transport.Capabilities().Restartable {
		return datatransfer.ErrUnsupported
	}

	result, err := m.validateRestart(channel)
	if err != nil {
		return xerrors.Errorf("failed to restart channel, validation error: %w", err)
	}

	if !result.Accepted {
		return datatransfer.ErrRejected
	}

	// send a libp2p message to the other peer asking to send a "restart push request"
	req := message.RestartExistingChannelRequest(channel.ChannelID())

	if err := m.transport.SendMessage(ctx, channel.ChannelID(), req); err != nil {
		return xerrors.Errorf("unable to send restart request: %w", err)
	}
	return nil
}

func (m *manager) openRestartChannel(ctx context.Context, channel datatransfer.ChannelState) error {
	selector := channel.Selector()
	voucher, err := channel.Voucher()
	if err != nil {
		return err
	}
	baseCid := channel.BaseCID()
	requestTo := channel.OtherPeer()
	chid := channel.ChannelID()

	if !m.transport.Capabilities().Restartable {
		return datatransfer.ErrUnsupported
	}
	req, err := message.NewRequest(chid.ID, true, channel.IsPull(), &voucher, baseCid, selector)
	if err != nil {
		return err
	}

	processor, has := m.transportConfigurers.Processor(voucher.Type)
	if has {
		transportConfigurer := processor.(datatransfer.TransportConfigurer)
		transportConfigurer(chid, voucher, m.transport)
	}

	// Monitor the state of the connection for the channel
	monitoredChan := m.channelMonitor.AddChannel(chid, channel.IsPull())
	log.Infof("sending push restart channel to %s for channel %s", requestTo, chid)
	err = m.transport.RestartChannel(ctx, channel, req)
	if err != nil {
		// If pull channel monitoring is enabled, shutdown the monitor as it
		// wasn't possible to start the data transfer
		if monitoredChan != nil {
			monitoredChan.Shutdown()
		}

		return xerrors.Errorf("Unable to send open channel restart request: %w", err)
	}

	return nil
}

func (m *manager) validateRestartRequest(ctx context.Context, otherPeer peer.ID, channel datatransfer.ChannelState, req datatransfer.Request) error {

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

	// vouchers should match
	reqVoucher, err := req.Voucher()
	if err != nil {
		return xerrors.Errorf("failed to fetch request voucher: %w", err)
	}
	channelVoucher, err := channel.Voucher()
	if err != nil {
		return xerrors.Errorf("failed to fetch channel voucher: %w", err)
	}
	if req.VoucherType() != channelVoucher.Type {
		return xerrors.New("channel and request voucher types do not match")
	}

	if !ipld.DeepEqual(reqVoucher, channelVoucher.Voucher) {
		return xerrors.New("channel and request vouchers do not match")
	}

	return nil
}
