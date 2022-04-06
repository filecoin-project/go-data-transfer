package message

import (
	message1_1 "github.com/filecoin-project/go-data-transfer/v2/message/message1_1prime"
)

var NewRequest = message1_1.NewRequest
var RestartExistingChannelRequest = message1_1.RestartExistingChannelRequest
var UpdateRequest = message1_1.UpdateRequest
var VoucherRequest = message1_1.VoucherRequest
var NewResponse = message1_1.NewResponse
var CancelResponse = message1_1.CancelResponse
var UpdateResponse = message1_1.UpdateResponse
var FromNet = message1_1.FromNet
var FromIPLD = message1_1.FromIPLD
var CancelRequest = message1_1.CancelRequest
