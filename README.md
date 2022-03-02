# go-data-transfer
[![](https://img.shields.io/badge/made%20by-Protocol%20Labs-blue.svg?style=flat-square)](http://ipn.io)
[![CircleCI](https://circleci.com/gh/filecoin-project/go-data-transfer.svg?style=svg)](https://circleci.com/gh/filecoin-project/go-data-transfer)
[![codecov](https://codecov.io/gh/filecoin-project/go-data-transfer/branch/master/graph/badge.svg)](https://codecov.io/gh/filecoin-project/go-data-transfer)

A go module to perform data transfers over [ipfs/go-graphsync](https://github.com/ipfs/go-graphsync)

## Description
This module encapsulates protocols for exchanging piece data between storage clients and miners, both when consummating a storage deal and when retrieving the piece later. 

## Table of Contents
* [Background](https://github.com/filecoin-project/go-data-transfer/tree/master#background)
* [Usage](https://github.com/filecoin-project/go-data-transfer/tree/master#usage)
    * [Initialize a data transfer module](https://github.com/filecoin-project/go-data-transfer/tree/master#initialize-a-data-transfer-module)
    * [Register a validator](https://github.com/filecoin-project/go-data-transfer/tree/master#register-a-validator)
    * [Open a Push or Pull Request](https://github.com/filecoin-project/go-data-transfer/tree/master#open-a-push-or-pull-request)
    * [Subscribe to Events](https://github.com/filecoin-project/go-data-transfer/tree/master#subscribe-to-events)
* [Contribute](https://github.com/filecoin-project/go-data-transfer/tree/master#contribute)

## Usage

**Requires go 1.13**

Install the module in your package or app with `go get "github.com/filecoin-project/go-data-transfer/datatransfer"`


### Initialize a data transfer module
1. Set up imports. You need, minimally, the following imports:
    ```go
    package mypackage

    import (
        gsimpl "github.com/ipfs/go-graphsync/impl"
        datatransfer "github.com/filecoin-project/go-data-transfer/impl"
        gstransport "github.com/filecoin-project/go-data-transfer/transport/graphsync"
        "github.com/libp2p/go-libp2p-core/host"
    )
            
    ```
1. Provide or create a [libp2p host.Host](https://github.com/libp2p/go-libp2p-examples/tree/master/libp2p-host)
1. You will need a transport protocol. The current default transport is graphsync. [go-graphsync GraphExchange](https://github.com/ipfs/go-graphsync#initializing-a-graphsync-exchange)
1. Create a data transfer by building a transport interface and then initializing a new data transfer instance
    ```go
    func NewGraphsyncDataTransfer(h host.Host, gs graphsync.GraphExchange) {
        tp := gstransport.NewTransport(h.ID(), gs)
        dt := impl.NewDataTransfer(h, tp)
    }
    ```

1. If needed, build out your voucher struct and its validator. 
    
    A push or pull request must include a voucher. The voucher's type must have been registered with 
    the node receiving the request before it's sent, otherwise the request will be rejected.  

    [datatransfer.Voucher](https://github.com/filecoin-project/go-data-transfer/blob/21dd66ba370176224114b13030ee68cb785fadb2/datatransfer/types.go#L17)
    and [datatransfer.Validator](https://github.com/filecoin-project/go-data-transfer/blob/21dd66ba370176224114b13030ee68cb785fadb2/datatransfer/types.go#L153)
    are the types used for validation of graphsync datatransfer messages. **Both of these are simply [ipld.Node](https://pkg.go.dev/github.com/ipld/go-ipld-prime#Node)s**.

#### Example Toy Voucher and Validator
```go
const myVoucherType = datatransfer.TypeIdentifier("myVoucher")

type myVoucher struct {
	data string
}

func fromNode(node ipld.Node) (myVoucher, error) {
	if node.Kind() != datamodel.Kind_String {
		return nil, fmt.Errorf("invalid node kind")
	}
	str, err := node.AsString()
	if err != nil {
		return nil, err
	}
	return myVoucher{data: str}, nil
}

func (m myVoucher) toNode() ipld.Node {
	return basicnode.NewString(m.data)
}

type myValidator struct {
	ctx                 context.Context
	ValidationsReceived chan receivedValidation
}

func (vl *myValidator) ValidatePush(
	sender peer.ID,
	voucherType datatransfer.TypeIdentifier,
	voucher datatransfer.Voucher,
	baseCid cid.Cid,
	selector ipld.Node) error {

	v, err := fromNode(voucher)
	if err != nil {
		return err
	}
	if v.data == "" || v.data != "validpush" {
			return errors.New("invalid")
	} 

	return nil
}

func (vl *myValidator) ValidatePull(
	receiver peer.ID,
	voucherType datatransfer.TypeIdentifier,
	voucher datatransfer.Voucher,
	baseCid cid.Cid,
	selector ipld.Node) error {

	v, err := fromNode(voucher)
	if err != nil {
		return err
	}
	if v.data == "" || v.data != "validpull" {
			return errors.New("invalid")
	} 

	return nil
}
```


Please see 
[go-data-transfer/blob/master/types.go](https://github.com/filecoin-project/go-data-transfer/blob/master/types.go) 
for more detail.


### Register a validator
Before sending push or pull requests, you must register a voucher type by its `datatransfer.TypeIdentifier` and `dataTransfer.RequestValidator` for vouchers that
must be sent with the request.  Using the trivial examples above:
```go
    func NewGraphsyncDatatransfer(h host.Host, gs graphsync.GraphExchange) {
        tp := gstransport.NewTransport(h.ID(), gs)
        dt := impl.NewDataTransfer(h, tp)

        mv := &myValidator{} 
        dt.RegisterVoucherType(myVoucherType, mv)
    }
```
    
For more detail, please see the [unit tests](https://github.com/filecoin-project/go-data-transfer/blob/master/impl/impl_test.go).

### Open a Push or Pull Request
For a push or pull request, provide a context, a voucher type `datatransfer.TypeIdentifier`, a `datatransfer.Voucher`, a host recipient `peer.ID`, a baseCID `cid.CID` and a selector `ipld.Node`.  These
calls return a `datatransfer.ChannelID` and any error:
```go
    channelID, err := dtm.OpenPullDataChannel(ctx, recipient, voucherType, voucher, baseCid, selector)
    // OR
    channelID, err := dtm.OpenPushDataChannel(ctx, recipient, voucherType, voucher, baseCid, selector)

```

### Subscribe to Events

The module allows the consumer to be notified when a graphsync Request is sent or a datatransfer push or pull request response is received:

```go
    func ToySubscriberFunc (event Event, channelState ChannelState) {
        if event.Code == datatransfer.Error {
            // log error, flail about helplessly
            return
        }
        // 
        if channelState.Recipient() == our.PeerID && channelState.Received() > 0 {
            // log some stuff, update some state somewhere, send data to a channel, etc.
        }
    }

    dtm := SetupDataTransferManager(ctx, h, gs, baseCid, snode)
    unsubFunc := dtm.SubscribeToEvents(ToySubscriberFunc)

    // . . . later, when you don't need to know about events any more:
    unsubFunc()
```

## Contributing
PRs are welcome!  Please first read the design docs and look over the current code.  PRs against 
master require approval of at least two maintainers.  For the rest, please see our 
[CONTRIBUTING](https://github.com/filecoin-project/go-data-transfer/CONTRIBUTING.md) guide.

## License
This repository is dual-licensed under Apache 2.0 and MIT terms.

Copyright 2019. Protocol Labs, Inc.
