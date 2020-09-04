package impl

import (
	"context"
	"fmt"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/channels"
	"github.com/filecoin-project/go-data-transfer/message"
	"github.com/ipfs/go-cid"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
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

	// recreate the request that would led to this push channel being created for validation
	req, err := message.NewRequest(false, chid.ID, false, channel.Voucher().Type(), channel.Voucher(),
		channel.BaseCID(), channel.Selector())
	if err != nil {
		return err
	}
	return m.restartAsReceiver(ctx, channel, req)
}

func (m *manager) restartAsReceiver(ctx context.Context, channel datatransfer.ChannelState, reqToValidate datatransfer.Request) error {
	received := channel.ReceivedCids()
	doNotSend := mkCidSet(received)
	selector := channel.Selector()
	voucher := channel.Voucher()
	baseCid := channel.BaseCID()
	requestTo := channel.OtherParty(m.peerID)
	chid := channel.ChannelID()

	// revalidate the voucher by reconstructing the request that would have led to the creation of this channel
	if reqToValidate != nil {
		if _, _, err := m.validateVoucher(chid.Initiator, reqToValidate, false, baseCid, selector); err != nil {
			return err
		}
	}

	req, err := m.restartRequest(channel.TransferID(), selector, true, voucher, baseCid)
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

func (m *manager) validateRestartChannelReq(ctx context.Context, chid datatransfer.ChannelID, req datatransfer.Request) (ChannelDataTransferType, error) {
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

	return m.channelDataTransferType(channel), nil
}

func mkCidSet(cids []cid.Cid) *cid.Set {
	set := cid.NewSet()
	for _, c := range cids {
		set.Add(c)
	}
	return set
}
