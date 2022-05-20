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
	voucher ipld.Node,
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
	voucher ipld.Node,
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

func (sv *StubbedValidator) ValidateRestart(chid datatransfer.ChannelID, channelState datatransfer.ChannelState) (datatransfer.ValidationResult, error) {
	sv.didRevalidate = true
	sv.RevalidationsReceived = append(sv.RevalidationsReceived, ReceivedRestartValidation{chid, channelState})
	return sv.revalidationResult, sv.revalidationError
}

// StubRestartResult returns the given voucher result when a call is made to ValidateRestart
func (sv *StubbedValidator) StubRestartResult(voucherResult datatransfer.ValidationResult) {
	sv.revalidationResult = voucherResult
}

// StubErrorValidateRestart sets ValidateRestart to error
func (sv *StubbedValidator) StubErrorValidateRestart() {
	sv.revalidationError = errors.New("something went wrong")
}

// StubSuccessValidateRestart sets ValidateRestart to succeed
func (sv *StubbedValidator) StubSuccessValidateRestart() {
	sv.revalidationError = nil
}

// ExpectErrorValidateRestart expects ValidateRestart to error
func (sv *StubbedValidator) ExpectErrorValidateRestart() {
	sv.expectRevalidate = true
	sv.StubErrorValidateRestart()
}

// ExpectSuccessValidateRestart expects ValidateRestart to succeed
func (sv *StubbedValidator) ExpectSuccessValidateRestart() {
	sv.expectRevalidate = true
	sv.StubSuccessValidateRestart()
}

// ReceivedValidation records a call to either ValidatePush or ValidatePull
type ReceivedValidation struct {
	IsPull   bool
	Other    peer.ID
	Voucher  ipld.Node
	BaseCid  cid.Cid
	Selector ipld.Node
}

// ReceivedRestartValidation records a call to ValidateRestart
type ReceivedRestartValidation struct {
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
	RevalidationsReceived []ReceivedRestartValidation
}

var _ datatransfer.RequestValidator = (*StubbedValidator)(nil)
