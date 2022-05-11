package testutil

import (
	"errors"
	"testing"

	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
)

// NewStubbedValidator returns a new instance of a stubbed validator
func NewStubbedValidator() *StubbedValidator {
	return &StubbedValidator{}
}

// ValidatePush returns a stubbed result for a push validation
func (sv *StubbedValidator) ValidatePush(
	chid datatransfer.ChannelID,
	sender peer.ID,
	voucher datatransfer.Voucher,
	baseCid cid.Cid,
	selector ipld.Node) (datatransfer.ValidationResult, error) {
	sv.didPush = true
	sv.ValidationsReceived = append(sv.ValidationsReceived, ReceivedValidation{false, sender, voucher, baseCid, selector})
	return sv.result, sv.pushError
}

// ValidatePull returns a stubbed result for a pull validation
func (sv *StubbedValidator) ValidatePull(
	chid datatransfer.ChannelID,
	receiver peer.ID,
	voucher datatransfer.Voucher,
	baseCid cid.Cid,
	selector ipld.Node) (datatransfer.ValidationResult, error) {
	sv.didPull = true
	sv.ValidationsReceived = append(sv.ValidationsReceived, ReceivedValidation{true, receiver, voucher, baseCid, selector})
	return sv.result, sv.pullError
}

// StubResult returns thes given voucher result when a validate call is made
func (sv *StubbedValidator) StubResult(voucherResult datatransfer.ValidationResult) {
	sv.result = voucherResult
}

// StubErrorPush sets ValidatePush to error
func (sv *StubbedValidator) StubErrorPush() {
	sv.pushError = errors.New("something went wrong")
}

// StubSuccessPush sets ValidatePush to succeed
func (sv *StubbedValidator) StubSuccessPush() {
	sv.pushError = nil
}

// ExpectErrorPush expects ValidatePush to error
func (sv *StubbedValidator) ExpectErrorPush() {
	sv.expectPush = true
	sv.StubErrorPush()
}

// ExpectSuccessPush expects ValidatePush to error
func (sv *StubbedValidator) ExpectSuccessPush() {
	sv.expectPush = true
	sv.StubSuccessPush()
}

// StubErrorPull sets ValidatePull to error
func (sv *StubbedValidator) StubErrorPull() {
	sv.pullError = errors.New("something went wrong")
}

// StubSuccessPull sets ValidatePull to succeed
func (sv *StubbedValidator) StubSuccessPull() {
	sv.pullError = nil
}

// ExpectErrorPull expects ValidatePull to error
func (sv *StubbedValidator) ExpectErrorPull() {
	sv.expectPull = true
	sv.StubErrorPull()
}

// ExpectSuccessPull expects ValidatePull to error
func (sv *StubbedValidator) ExpectSuccessPull() {
	sv.expectPull = true
	sv.StubSuccessPull()
}

// VerifyExpectations verifies the specified calls were made
func (sv *StubbedValidator) VerifyExpectations(t *testing.T) {
	if sv.expectPush {
		require.True(t, sv.didPush)
	}
	if sv.expectPull {
		require.True(t, sv.didPull)
	}
	if sv.expectRevalidate {
		require.True(t, sv.didRevalidate)
	}
}

func (sv *StubbedValidator) Revalidate(chid datatransfer.ChannelID, channelState datatransfer.ChannelState) (datatransfer.ValidationResult, error) {
	sv.didRevalidate = true
	sv.RevalidationsReceived = append(sv.RevalidationsReceived, ReceivedRevalidation{chid, channelState})
	return sv.revalidationResult, sv.revalidationError
}

// StubRevalidationResult returns the given voucher result when a call is made to Revalidate
func (sv *StubbedValidator) StubRevalidationResult(voucherResult datatransfer.ValidationResult) {
	sv.revalidationResult = voucherResult
}

// StubErrorRevalidation sets Revalidate to error
func (sv *StubbedValidator) StubErrorRevalidation() {
	sv.revalidationError = errors.New("something went wrong")
}

// StubSuccessRevalidation sets Revalidate to succeed
func (sv *StubbedValidator) StubSuccessRevalidation() {
	sv.revalidationError = nil
}

// ExpectErrorRevalidation expects Revalidate to error
func (sv *StubbedValidator) ExpectErrorRevalidation() {
	sv.expectRevalidate = true
	sv.StubErrorRevalidation()
}

// ExpectSuccessRevalidation expects Revalidate to succeed
func (sv *StubbedValidator) ExpectSuccessRevalidation() {
	sv.expectRevalidate = true
	sv.StubSuccessRevalidation()
}

// ReceivedValidation records a call to either ValidatePush or ValidatePull
type ReceivedValidation struct {
	IsPull   bool
	Other    peer.ID
	Voucher  datatransfer.Voucher
	BaseCid  cid.Cid
	Selector ipld.Node
}

// ReceivedRevalidation records a call to Revalidate
type ReceivedRevalidation struct {
	ChannelID    datatransfer.ChannelID
	ChannelState datatransfer.ChannelState
}

// StubbedValidator is a validator that returns predictable results
type StubbedValidator struct {
	result                datatransfer.ValidationResult
	revalidationResult    datatransfer.ValidationResult
	expectRevalidate      bool
	didRevalidate         bool
	didPush               bool
	didPull               bool
	expectPush            bool
	expectPull            bool
	pushError             error
	pullError             error
	revalidationError     error
	ValidationsReceived   []ReceivedValidation
	RevalidationsReceived []ReceivedRevalidation
}

var _ datatransfer.RequestValidator = (*StubbedValidator)(nil)
