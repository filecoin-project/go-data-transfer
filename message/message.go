package message

import (
	message1_1 "github.com/filecoin-project/go-data-transfer/v2/message/message1_1prime"
)

var NewRequest = message1_1.NewRequest
var RestartExistingChannelRequest = message1_1.RestartExistingChannelRequest
var UpdateRequest = message1_1.UpdateRequest
var VoucherRequest = message1_1.VoucherRequest

// DEPRECATED: Use ValidationResultResponse
var RestartResponse = message1_1.RestartResponse

var ValidationResultResponse = message1_1.ValidationResultResponse

// DEPRECATED: Use ValidationResultResponse
var NewResponse = message1_1.NewResponse

// DEPRECATED: Use ValidationResultResponse
var VoucherResultResponse = message1_1.VoucherResultResponse
var CancelResponse = message1_1.CancelResponse
var UpdateResponse = message1_1.UpdateResponse
var FromNet = message1_1.FromNet
var FromIPLD = message1_1.FromIPLD
var CompleteResponse = message1_1.CompleteResponse
var CancelRequest = message1_1.CancelRequest
