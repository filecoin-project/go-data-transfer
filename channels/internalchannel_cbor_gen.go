// Code generated by github.com/whyrusleeping/cbor-gen. DO NOT EDIT.

package channels

import (
	"fmt"
	"io"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/ipfs/go-cid"
	peer "github.com/libp2p/go-libp2p-core/peer"
	cbg "github.com/whyrusleeping/cbor-gen"
	xerrors "golang.org/x/xerrors"
)

var _ = xerrors.Errorf

var lengthBufinternalChannelState = []byte{144}

func (t *internalChannelState) MarshalCBOR(w io.Writer) error {
	if t == nil {
		_, err := w.Write(cbg.CborNull)
		return err
	}
	if _, err := w.Write(lengthBufinternalChannelState); err != nil {
		return err
	}

	scratch := make([]byte, 9)

	// t.SelfPeer (peer.ID) (string)
	if len(t.SelfPeer) > cbg.MaxLength {
		return xerrors.Errorf("Value in field t.SelfPeer was too long")
	}

	if err := cbg.WriteMajorTypeHeaderBuf(scratch, w, cbg.MajTextString, uint64(len(t.SelfPeer))); err != nil {
		return err
	}
	if _, err := io.WriteString(w, string(t.SelfPeer)); err != nil {
		return err
	}

	// t.TransferID (datatransfer.TransferID) (uint64)

	if err := cbg.WriteMajorTypeHeaderBuf(scratch, w, cbg.MajUnsignedInt, uint64(t.TransferID)); err != nil {
		return err
	}

	// t.Initiator (peer.ID) (string)
	if len(t.Initiator) > cbg.MaxLength {
		return xerrors.Errorf("Value in field t.Initiator was too long")
	}

	if err := cbg.WriteMajorTypeHeaderBuf(scratch, w, cbg.MajTextString, uint64(len(t.Initiator))); err != nil {
		return err
	}
	if _, err := io.WriteString(w, string(t.Initiator)); err != nil {
		return err
	}

	// t.Responder (peer.ID) (string)
	if len(t.Responder) > cbg.MaxLength {
		return xerrors.Errorf("Value in field t.Responder was too long")
	}

	if err := cbg.WriteMajorTypeHeaderBuf(scratch, w, cbg.MajTextString, uint64(len(t.Responder))); err != nil {
		return err
	}
	if _, err := io.WriteString(w, string(t.Responder)); err != nil {
		return err
	}

	// t.BaseCid (cid.Cid) (struct)

	if err := cbg.WriteCidBuf(scratch, w, t.BaseCid); err != nil {
		return xerrors.Errorf("failed to write cid field t.BaseCid: %w", err)
	}

	// t.Selector (typegen.Deferred) (struct)
	if err := t.Selector.MarshalCBOR(w); err != nil {
		return err
	}

	// t.Sender (peer.ID) (string)
	if len(t.Sender) > cbg.MaxLength {
		return xerrors.Errorf("Value in field t.Sender was too long")
	}

	if err := cbg.WriteMajorTypeHeaderBuf(scratch, w, cbg.MajTextString, uint64(len(t.Sender))); err != nil {
		return err
	}
	if _, err := io.WriteString(w, string(t.Sender)); err != nil {
		return err
	}

	// t.Recipient (peer.ID) (string)
	if len(t.Recipient) > cbg.MaxLength {
		return xerrors.Errorf("Value in field t.Recipient was too long")
	}

	if err := cbg.WriteMajorTypeHeaderBuf(scratch, w, cbg.MajTextString, uint64(len(t.Recipient))); err != nil {
		return err
	}
	if _, err := io.WriteString(w, string(t.Recipient)); err != nil {
		return err
	}

	// t.TotalSize (uint64) (uint64)

	if err := cbg.WriteMajorTypeHeaderBuf(scratch, w, cbg.MajUnsignedInt, uint64(t.TotalSize)); err != nil {
		return err
	}

	// t.Status (datatransfer.Status) (uint64)

	if err := cbg.WriteMajorTypeHeaderBuf(scratch, w, cbg.MajUnsignedInt, uint64(t.Status)); err != nil {
		return err
	}

	// t.Sent (uint64) (uint64)

	if err := cbg.WriteMajorTypeHeaderBuf(scratch, w, cbg.MajUnsignedInt, uint64(t.Sent)); err != nil {
		return err
	}

	// t.Received (uint64) (uint64)

	if err := cbg.WriteMajorTypeHeaderBuf(scratch, w, cbg.MajUnsignedInt, uint64(t.Received)); err != nil {
		return err
	}

	// t.Message (string) (string)
	if len(t.Message) > cbg.MaxLength {
		return xerrors.Errorf("Value in field t.Message was too long")
	}

	if err := cbg.WriteMajorTypeHeaderBuf(scratch, w, cbg.MajTextString, uint64(len(t.Message))); err != nil {
		return err
	}
	if _, err := io.WriteString(w, string(t.Message)); err != nil {
		return err
	}

	// t.Vouchers ([]channels.encodedVoucher) (slice)
	if len(t.Vouchers) > cbg.MaxLength {
		return xerrors.Errorf("Slice value in field t.Vouchers was too long")
	}

	if err := cbg.WriteMajorTypeHeaderBuf(scratch, w, cbg.MajArray, uint64(len(t.Vouchers))); err != nil {
		return err
	}
	for _, v := range t.Vouchers {
		if err := v.MarshalCBOR(w); err != nil {
			return err
		}
	}

	// t.VoucherResults ([]channels.encodedVoucherResult) (slice)
	if len(t.VoucherResults) > cbg.MaxLength {
		return xerrors.Errorf("Slice value in field t.VoucherResults was too long")
	}

	if err := cbg.WriteMajorTypeHeaderBuf(scratch, w, cbg.MajArray, uint64(len(t.VoucherResults))); err != nil {
		return err
	}
	for _, v := range t.VoucherResults {
		if err := v.MarshalCBOR(w); err != nil {
			return err
		}
	}

	// t.ReceivedCids ([]cid.Cid) (slice)
	if len(t.ReceivedCids) > cbg.MaxLength {
		return xerrors.Errorf("Slice value in field t.ReceivedCids was too long")
	}

	if err := cbg.WriteMajorTypeHeaderBuf(scratch, w, cbg.MajArray, uint64(len(t.ReceivedCids))); err != nil {
		return err
	}
	for _, v := range t.ReceivedCids {
		if err := cbg.WriteCidBuf(scratch, w, v); err != nil {
			return xerrors.Errorf("failed writing cid field t.ReceivedCids: %w", err)
		}
	}
	return nil
}

func (t *internalChannelState) UnmarshalCBOR(r io.Reader) error {
	*t = internalChannelState{}

	br := cbg.GetPeeker(r)
	scratch := make([]byte, 8)

	maj, extra, err := cbg.CborReadHeaderBuf(br, scratch)
	if err != nil {
		return err
	}
	if maj != cbg.MajArray {
		return fmt.Errorf("cbor input should be of type array")
	}

	if extra != 16 {
		return fmt.Errorf("cbor input had wrong number of fields")
	}

	// t.SelfPeer (peer.ID) (string)

	{
		sval, err := cbg.ReadStringBuf(br, scratch)
		if err != nil {
			return err
		}

		t.SelfPeer = peer.ID(sval)
	}
	// t.TransferID (datatransfer.TransferID) (uint64)

	{

		maj, extra, err = cbg.CborReadHeaderBuf(br, scratch)
		if err != nil {
			return err
		}
		if maj != cbg.MajUnsignedInt {
			return fmt.Errorf("wrong type for uint64 field")
		}
		t.TransferID = datatransfer.TransferID(extra)

	}
	// t.Initiator (peer.ID) (string)

	{
		sval, err := cbg.ReadStringBuf(br, scratch)
		if err != nil {
			return err
		}

		t.Initiator = peer.ID(sval)
	}
	// t.Responder (peer.ID) (string)

	{
		sval, err := cbg.ReadStringBuf(br, scratch)
		if err != nil {
			return err
		}

		t.Responder = peer.ID(sval)
	}
	// t.BaseCid (cid.Cid) (struct)

	{

		c, err := cbg.ReadCid(br)
		if err != nil {
			return xerrors.Errorf("failed to read cid field t.BaseCid: %w", err)
		}

		t.BaseCid = c

	}
	// t.Selector (typegen.Deferred) (struct)

	{

		t.Selector = new(cbg.Deferred)

		if err := t.Selector.UnmarshalCBOR(br); err != nil {
			return xerrors.Errorf("failed to read deferred field: %w", err)
		}
	}
	// t.Sender (peer.ID) (string)

	{
		sval, err := cbg.ReadStringBuf(br, scratch)
		if err != nil {
			return err
		}

		t.Sender = peer.ID(sval)
	}
	// t.Recipient (peer.ID) (string)

	{
		sval, err := cbg.ReadStringBuf(br, scratch)
		if err != nil {
			return err
		}

		t.Recipient = peer.ID(sval)
	}
	// t.TotalSize (uint64) (uint64)

	{

		maj, extra, err = cbg.CborReadHeaderBuf(br, scratch)
		if err != nil {
			return err
		}
		if maj != cbg.MajUnsignedInt {
			return fmt.Errorf("wrong type for uint64 field")
		}
		t.TotalSize = uint64(extra)

	}
	// t.Status (datatransfer.Status) (uint64)

	{

		maj, extra, err = cbg.CborReadHeaderBuf(br, scratch)
		if err != nil {
			return err
		}
		if maj != cbg.MajUnsignedInt {
			return fmt.Errorf("wrong type for uint64 field")
		}
		t.Status = datatransfer.Status(extra)

	}
	// t.Sent (uint64) (uint64)

	{

		maj, extra, err = cbg.CborReadHeaderBuf(br, scratch)
		if err != nil {
			return err
		}
		if maj != cbg.MajUnsignedInt {
			return fmt.Errorf("wrong type for uint64 field")
		}
		t.Sent = uint64(extra)

	}
	// t.Received (uint64) (uint64)

	{

		maj, extra, err = cbg.CborReadHeaderBuf(br, scratch)
		if err != nil {
			return err
		}
		if maj != cbg.MajUnsignedInt {
			return fmt.Errorf("wrong type for uint64 field")
		}
		t.Received = uint64(extra)

	}
	// t.Message (string) (string)

	{
		sval, err := cbg.ReadStringBuf(br, scratch)
		if err != nil {
			return err
		}

		t.Message = string(sval)
	}
	// t.Vouchers ([]channels.encodedVoucher) (slice)

	maj, extra, err = cbg.CborReadHeaderBuf(br, scratch)
	if err != nil {
		return err
	}

	if extra > cbg.MaxLength {
		return fmt.Errorf("t.Vouchers: array too large (%d)", extra)
	}

	if maj != cbg.MajArray {
		return fmt.Errorf("expected cbor array")
	}

	if extra > 0 {
		t.Vouchers = make([]encodedVoucher, extra)
	}

	for i := 0; i < int(extra); i++ {

		var v encodedVoucher
		if err := v.UnmarshalCBOR(br); err != nil {
			return err
		}

		t.Vouchers[i] = v
	}

	// t.VoucherResults ([]channels.encodedVoucherResult) (slice)

	maj, extra, err = cbg.CborReadHeaderBuf(br, scratch)
	if err != nil {
		return err
	}

	if extra > cbg.MaxLength {
		return fmt.Errorf("t.VoucherResults: array too large (%d)", extra)
	}

	if maj != cbg.MajArray {
		return fmt.Errorf("expected cbor array")
	}

	if extra > 0 {
		t.VoucherResults = make([]encodedVoucherResult, extra)
	}

	for i := 0; i < int(extra); i++ {

		var v encodedVoucherResult
		if err := v.UnmarshalCBOR(br); err != nil {
			return err
		}

		t.VoucherResults[i] = v
	}

	// t.ReceivedCids ([]cid.Cid) (slice)

	maj, extra, err = cbg.CborReadHeaderBuf(br, scratch)
	if err != nil {
		return err
	}

	if extra > cbg.MaxLength {
		return fmt.Errorf("t.ReceivedCids: array too large (%d)", extra)
	}

	if maj != cbg.MajArray {
		return fmt.Errorf("expected cbor array")
	}

	if extra > 0 {
		t.ReceivedCids = make([]cid.Cid, extra)
	}

	for i := 0; i < int(extra); i++ {

		c, err := cbg.ReadCid(br)
		if err != nil {
			return xerrors.Errorf("reading cid field t.ReceivedCids failed: %w", err)
		}
		t.ReceivedCids[i] = c
	}

	return nil
}

var lengthBufencodedVoucher = []byte{130}

func (t *encodedVoucher) MarshalCBOR(w io.Writer) error {
	if t == nil {
		_, err := w.Write(cbg.CborNull)
		return err
	}
	if _, err := w.Write(lengthBufencodedVoucher); err != nil {
		return err
	}

	scratch := make([]byte, 9)

	// t.Type (datatransfer.TypeIdentifier) (string)
	if len(t.Type) > cbg.MaxLength {
		return xerrors.Errorf("Value in field t.Type was too long")
	}

	if err := cbg.WriteMajorTypeHeaderBuf(scratch, w, cbg.MajTextString, uint64(len(t.Type))); err != nil {
		return err
	}
	if _, err := io.WriteString(w, string(t.Type)); err != nil {
		return err
	}

	// t.Voucher (typegen.Deferred) (struct)
	if err := t.Voucher.MarshalCBOR(w); err != nil {
		return err
	}
	return nil
}

func (t *encodedVoucher) UnmarshalCBOR(r io.Reader) error {
	*t = encodedVoucher{}

	br := cbg.GetPeeker(r)
	scratch := make([]byte, 8)

	maj, extra, err := cbg.CborReadHeaderBuf(br, scratch)
	if err != nil {
		return err
	}
	if maj != cbg.MajArray {
		return fmt.Errorf("cbor input should be of type array")
	}

	if extra != 2 {
		return fmt.Errorf("cbor input had wrong number of fields")
	}

	// t.Type (datatransfer.TypeIdentifier) (string)

	{
		sval, err := cbg.ReadStringBuf(br, scratch)
		if err != nil {
			return err
		}

		t.Type = datatransfer.TypeIdentifier(sval)
	}
	// t.Voucher (typegen.Deferred) (struct)

	{

		t.Voucher = new(cbg.Deferred)

		if err := t.Voucher.UnmarshalCBOR(br); err != nil {
			return xerrors.Errorf("failed to read deferred field: %w", err)
		}
	}
	return nil
}

var lengthBufencodedVoucherResult = []byte{130}

func (t *encodedVoucherResult) MarshalCBOR(w io.Writer) error {
	if t == nil {
		_, err := w.Write(cbg.CborNull)
		return err
	}
	if _, err := w.Write(lengthBufencodedVoucherResult); err != nil {
		return err
	}

	scratch := make([]byte, 9)

	// t.Type (datatransfer.TypeIdentifier) (string)
	if len(t.Type) > cbg.MaxLength {
		return xerrors.Errorf("Value in field t.Type was too long")
	}

	if err := cbg.WriteMajorTypeHeaderBuf(scratch, w, cbg.MajTextString, uint64(len(t.Type))); err != nil {
		return err
	}
	if _, err := io.WriteString(w, string(t.Type)); err != nil {
		return err
	}

	// t.VoucherResult (typegen.Deferred) (struct)
	if err := t.VoucherResult.MarshalCBOR(w); err != nil {
		return err
	}
	return nil
}

func (t *encodedVoucherResult) UnmarshalCBOR(r io.Reader) error {
	*t = encodedVoucherResult{}

	br := cbg.GetPeeker(r)
	scratch := make([]byte, 8)

	maj, extra, err := cbg.CborReadHeaderBuf(br, scratch)
	if err != nil {
		return err
	}
	if maj != cbg.MajArray {
		return fmt.Errorf("cbor input should be of type array")
	}

	if extra != 2 {
		return fmt.Errorf("cbor input had wrong number of fields")
	}

	// t.Type (datatransfer.TypeIdentifier) (string)

	{
		sval, err := cbg.ReadStringBuf(br, scratch)
		if err != nil {
			return err
		}

		t.Type = datatransfer.TypeIdentifier(sval)
	}
	// t.VoucherResult (typegen.Deferred) (struct)

	{

		t.VoucherResult = new(cbg.Deferred)

		if err := t.VoucherResult.UnmarshalCBOR(br); err != nil {
			return xerrors.Errorf("failed to read deferred field: %w", err)
		}
	}
	return nil
}
