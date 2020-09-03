package impl

import (
	"context"
	"fmt"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/ipfs/go-cid"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
)

type RestartChannelType int

const (
	ManagerPeerCreatePull RestartChannelType = iota

	ManagerPeerCreatePush

	ManagerPeerReceivePull

	ManagerPeerReceivePush
)

func (m *manager) restartManagerPeerCreatePull(ctx context.Context, channel datatransfer.ChannelState) error {
	received := channel.ReceivedCids()
	doNotSend := mkCidSet(received)
	selector := channel.Selector()
	voucher := channel.Voucher()
	baseCid := channel.BaseCID()
	requestTo := channel.OtherParty(m.peerID)
	chid := channel.ChannelID()

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

func mkCidSet(cids []cid.Cid) *cid.Set {
	set := cid.NewSet()
	for _, c := range cids {
		set.Add(c)
	}
	return set
}
