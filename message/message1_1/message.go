package message1_1

import (
	"io"

	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime/datamodel"
	cborgen "github.com/whyrusleeping/cbor-gen"
	xerrors "golang.org/x/xerrors"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/encoding"
	"github.com/filecoin-project/go-data-transfer/message/types"
)

// NewRequest generates a new request for the data transfer protocol
func NewRequest(id datatransfer.TransferID, isRestart bool, isPull bool, voucher datatransfer.Registerable, baseCid cid.Cid, selector datamodel.Node) (datatransfer.Request, error) {
	if baseCid == cid.Undef {
		return nil, xerrors.Errorf("base CID must be defined")
	}
	var typ int64
	if isRestart {
		typ = int64(types.RestartMessage)
	} else {
		typ = int64(types.NewMessage)
	}

	return &transferRequest1_1{
		Type:   typ,
		Pull:   isPull,
		Vouch:  voucher.Representation(),
		Stor:   selector,
		BCid:   &baseCid,
		VTyp:   v.type,
		XferID: int64(id),
	}, nil
}

// RestartExistingChannelRequest creates a request to ask the other side to restart an existing channel
func RestartExistingChannelRequest(channelId datatransfer.ChannelID) datatransfer.Request {

	return &transferRequest1_1{Type: uint64(types.RestartExistingChannelRequestMessage),
		RestartChannel: channelId}
}

// CancelRequest request generates a request to cancel an in progress request
func CancelRequest(id datatransfer.TransferID) datatransfer.Request {
	return &transferRequest1_1{
		Type:   uint64(types.CancelMessage),
		XferID: uint64(id),
	}
}

// UpdateRequest generates a new request update
func UpdateRequest(id datatransfer.TransferID, isPaused bool) datatransfer.Request {
	return &transferRequest1_1{
		Type:   uint64(types.UpdateMessage),
		Paus:   isPaused,
		XferID: uint64(id),
	}
}

// VoucherRequest generates a new request for the data transfer protocol
func VoucherRequest(id datatransfer.TransferID, vtype datatransfer.TypeIdentifier, voucher encoding.Encodable) (datatransfer.Request, error) {
	vbytes, err := encoding.Encode(voucher)
	if err != nil {
		return nil, xerrors.Errorf("Creating request: %w", err)
	}
	return &transferRequest1_1{
		Type:   uint64(types.VoucherMessage),
		Vouch:  &cborgen.Deferred{Raw: vbytes},
		VTyp:   vtype,
		XferID: uint64(id),
	}, nil
}

// RestartResponse builds a new Data Transfer response
func RestartResponse(id datatransfer.TransferID, accepted bool, isPaused bool, voucherResultType datatransfer.TypeIdentifier, voucherResult encoding.Encodable) (datatransfer.Response, error) {
	vbytes, err := encoding.Encode(voucherResult)
	if err != nil {
		return nil, xerrors.Errorf("Creating request: %w", err)
	}
	return &transferResponse1_1{
		Acpt:   accepted,
		Type:   uint64(types.RestartMessage),
		Paus:   isPaused,
		XferID: uint64(id),
		VTyp:   voucherResultType,
		VRes:   &cborgen.Deferred{Raw: vbytes},
	}, nil
}

// NewResponse builds a new Data Transfer response
func NewResponse(id datatransfer.TransferID, accepted bool, isPaused bool, voucherResultType datatransfer.TypeIdentifier, voucherResult encoding.Encodable) (datatransfer.Response, error) {
	vbytes, err := encoding.Encode(voucherResult)
	if err != nil {
		return nil, xerrors.Errorf("Creating request: %w", err)
	}
	return &transferResponse1_1{
		Acpt:   accepted,
		Type:   uint64(types.NewMessage),
		Paus:   isPaused,
		XferID: uint64(id),
		VTyp:   voucherResultType,
		VRes:   &cborgen.Deferred{Raw: vbytes},
	}, nil
}

// VoucherResultResponse builds a new response for a voucher result
func VoucherResultResponse(id datatransfer.TransferID, accepted bool, isPaused bool, voucherResultType datatransfer.TypeIdentifier, voucherResult encoding.Encodable) (datatransfer.Response, error) {
	vbytes, err := encoding.Encode(voucherResult)
	if err != nil {
		return nil, xerrors.Errorf("Creating request: %w", err)
	}
	return &transferResponse1_1{
		Acpt:   accepted,
		Type:   uint64(types.VoucherResultMessage),
		Paus:   isPaused,
		XferID: uint64(id),
		VTyp:   voucherResultType,
		VRes:   &cborgen.Deferred{Raw: vbytes},
	}, nil
}

// UpdateResponse returns a new update response
func UpdateResponse(id datatransfer.TransferID, isPaused bool) datatransfer.Response {
	return &transferResponse1_1{
		Type:   uint64(types.UpdateMessage),
		Paus:   isPaused,
		XferID: uint64(id),
	}
}

// CancelResponse makes a new cancel response message
func CancelResponse(id datatransfer.TransferID) datatransfer.Response {
	return &transferResponse1_1{
		Type:   uint64(types.CancelMessage),
		XferID: uint64(id),
	}
}

// CompleteResponse returns a new complete response message
func CompleteResponse(id datatransfer.TransferID, isAccepted bool, isPaused bool, voucherResultType datatransfer.TypeIdentifier, voucherResult encoding.Encodable) (datatransfer.Response, error) {
	vbytes, err := encoding.Encode(voucherResult)
	if err != nil {
		return nil, xerrors.Errorf("Creating request: %w", err)
	}
	return &transferResponse1_1{
		Type:   uint64(types.CompleteMessage),
		Acpt:   isAccepted,
		Paus:   isPaused,
		VTyp:   voucherResultType,
		VRes:   &cborgen.Deferred{Raw: vbytes},
		XferID: uint64(id),
	}, nil
}

// FromNet can read a network stream to deserialize a GraphSyncMessage
func FromNet(r io.Reader) (datatransfer.Message, error) {
	tresp := transferMessage1_1{}
	err := tresp.UnmarshalCBOR(r)
	if err != nil {
		return nil, err
	}

	if (tresp.IsRequest() && tresp.Request == nil) || (!tresp.IsRequest() && tresp.Response == nil) {
		return nil, xerrors.Errorf("invalid/malformed message")
	}

	if tresp.IsRequest() {
		return tresp.Request, nil
	}
	return tresp.Response, nil
}
