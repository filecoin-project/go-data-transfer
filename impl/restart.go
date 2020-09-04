package impl

import (
	"context"
	"fmt"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/channels"
	"github.com/filecoin-project/go-data-transfer/message"
	"github.com/ipfs/go-cid"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/libp2p/go-libp2p-core/peer"
	"golang.org/x/xerrors"
)

type ChannelDataTransferType int

const (
	// ManagerPeerCreatePull is the type of a channel wherein the manager peer created a Pull Data Transfer
	ManagerPeerCreatePull ChannelDataTransferType = iota

	// ManagerPeerCreatePush is the type of a channel wherein the manager peer created a Push Data Transfer
	ManagerPeerCreatePush

	// ManagerPeerCreatePush is the type of a channel wherein the manager peer received a Pull Data Transfer Request
	ManagerPeerReceivePull

	// ManagerPeerCreatePush is the type of a channel wherein the manager peer received a Push Data Transfer Request
	ManagerPeerReceivePush
)

func (m *manager) restartManagerPeerReceivePush(ctx context.Context, channel datatransfer.ChannelState) error {
	chid := channel.ChannelID()
	sender := channel.OtherParty(m.peerID)

	// recreate the request that would led to this push channel being created for validation
	req, err := message.RestartRequest(chid, false, channel.Voucher().Type(), channel.Voucher(),
		channel.BaseCID(), channel.Selector())
	if err != nil {
		return err
	}

	// revalidate the voucher by reconstructing the request that would have led to the creation of this channel
	if _, _, err := m.validateVoucher(sender, req, false, channel.BaseCID(), channel.Selector()); err != nil {
		return err
	}

	return m.restartAsReceiver(ctx, channel)
}

func (m *manager) restartAsReceiver(ctx context.Context, channel datatransfer.ChannelState) error {
	received := channel.ReceivedCids()
	doNotSend := mkCidSet(received)
	selector := channel.Selector()
	voucher := channel.Voucher()
	baseCid := channel.BaseCID()
	requestTo := channel.OtherParty(m.peerID)
	chid := channel.ChannelID()

	req, err := message.RestartRequest(chid, true, voucher.Type(), voucher, baseCid, selector)
	if err != nil {
		return err
	}

	m.dataTransferNetwork.Protect(requestTo, chid.String())

	processor, has := m.transportConfigurers.Processor(voucher.Type())
	if has {
		transportConfigurer := processor.(datatransfer.TransportConfigurer)
		transportConfigurer(chid, voucher, m.transport)
	}

	if err := m.transport.OpenChannel(ctx, requestTo, chid, cidlink.Link{Cid: baseCid}, selector, doNotSend, req); err != nil {
		err = fmt.Errorf("Unable to send request: %w", err)
		_ = m.channels.Error(chid, err)
		return err
	}

	return nil
}

/*func (m *manager) restartAsSender(ctx context.Context, channel datatransfer.ChannelState, reqToValidate datatransfer.Request) error {
	received := channel.ReceivedCids()
	doNotSend := mkCidSet(received)
	selector := channel.Selector()
	voucher := channel.Voucher()
	baseCid := channel.BaseCID()
	requestTo := channel.OtherParty(m.peerID)
	chid := channel.ChannelID()

	// revalidate the voucher by reconstructing the request that would have led to the creation of this channel
	if reqToValidate != nil {
		if _, _, err := m.validateVoucher(m.peerID, reqToValidate, false, baseCid, selector); err != nil {
			return err
		}
	}
}*/

func (m *manager) validateRestartChannelReq(ctx context.Context, otherPeer peer.ID, chid datatransfer.ChannelID, req datatransfer.Request) (ChannelDataTransferType, error) {
	channel, err := m.channels.GetByID(ctx, chid)
	if err != nil {
		return 0, xerrors.Errorf("failed to fetch channel: %w", err)
	}

	if channels.IsChannelTerminated(channel.Status()) {
		return 0, xerrors.New("channel is terminated")
	}

	// channel and request params should match
	if req.BaseCid() != channel.BaseCID() {
		return 0, xerrors.New("base cid does not match")
	}

	reqVoucher, err := m.decodeVoucher(req, m.validatedTypes)
	if err != nil {
		return 0, xerrors.Errorf("failed to decode request voucher: %w", err)
	}

	if reqVoucher != channel.Voucher() {
		return 0, xerrors.New("vouchers do not match")
	}

	sender := channel.Sender()
	recipient := channel.Recipient()
	initiator := channel.ChannelID().Initiator

	chType := m.channelDataTransferType(channel)
	switch chType {
	case ManagerPeerCreatePush:
		if initiator != m.peerID || sender != m.peerID || recipient != otherPeer {
			return 0, xerrors.New("peers do not match")
		}
	case ManagerPeerReceivePull:
		if initiator != otherPeer || sender != m.peerID || recipient != otherPeer {
			return 0, xerrors.New("peers do not match")
		}
		// 	TODO more states
	}

	return chType, nil
}

func (m *manager) validateRestartChannelResp(ctx context.Context, otherPeer peer.ID, chid datatransfer.ChannelID, resp datatransfer.Response) error {
	channel, err := m.channels.GetByID(ctx, chid)
	if err != nil {
		return xerrors.Errorf("failed to fetch channel: %w", err)
	}

	if channels.IsChannelTerminated(channel.Status()) {
		return xerrors.New("channel is terminated")
	}

	sender := channel.Sender()
	recipient := channel.Recipient()
	initiator := channel.ChannelID().Initiator

	chType := m.channelDataTransferType(channel)
	switch chType {
	case ManagerPeerCreatePush:
		if initiator != m.peerID || sender != m.peerID || recipient != otherPeer {
			return 0, xerrors.New("peers do not match")
		}
	case ManagerPeerReceivePull:
		if initiator != otherPeer || sender != m.peerID || recipient != otherPeer {
			return 0, xerrors.New("peers do not match")
		}
		// 	TODO more states
	}

	return nil
}

func mkCidSet(cids []cid.Cid) *cid.Set {
	set := cid.NewSet()
	for _, c := range cids {
		set.Add(c)
	}
	return set
}
