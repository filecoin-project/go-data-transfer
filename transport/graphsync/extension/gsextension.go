package extension

import (
	"bytes"
	"errors"
	"io"

	"github.com/ipfs/go-graphsync"
	"github.com/ipld/go-ipld-prime/codec/dagcbor"
	"github.com/ipld/go-ipld-prime/datamodel"
	basicnode "github.com/ipld/go-ipld-prime/node/basic"
	"github.com/libp2p/go-libp2p-core/protocol"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-data-transfer/message"
)

const (
	// ExtensionIncomingRequest1_1 is the identifier for data sent by the IncomingRequest hook
	ExtensionIncomingRequest1_1 = graphsync.ExtensionName("fil/data-transfer/incoming-request/1.1")
	// ExtensionOutgoingBlock1_1 is the identifier for data sent by the OutgoingBlock hook
	ExtensionOutgoingBlock1_1 = graphsync.ExtensionName("fil/data-transfer/outgoing-block/1.1")
	// ExtensionDataTransfer1_1 is the identifier for the v1.1 data transfer extension to graphsync
	ExtensionDataTransfer1_1 = graphsync.ExtensionName("fil/data-transfer/1.1")
)

// ProtocolMap maps graphsync extensions to their libp2p protocols
var ProtocolMap = map[graphsync.ExtensionName]protocol.ID{
	ExtensionIncomingRequest1_1: datatransfer.ProtocolDataTransfer1_2,
	ExtensionOutgoingBlock1_1:   datatransfer.ProtocolDataTransfer1_2,
	ExtensionDataTransfer1_1:    datatransfer.ProtocolDataTransfer1_2,
}

// ToExtensionData converts a message to a graphsync extension
func ToExtensionData(msg datatransfer.Message, supportedExtensions []graphsync.ExtensionName) ([]graphsync.ExtensionData, error) {
	exts := make([]graphsync.ExtensionData, 0, len(supportedExtensions))
	for _, supportedExtension := range supportedExtensions {
		protoID, ok := ProtocolMap[supportedExtension]
		if !ok {
			return nil, errors.New("unsupported protocol")
		}
		versionedMsg, err := msg.MessageForProtocol(protoID)
		if err != nil {
			continue
		}
		buf := new(bytes.Buffer)
		err = versionedMsg.ToNet(buf)
		if err != nil {
			return nil, err
		}
		nb := basicnode.Prototype.Any.NewBuilder()
		err = dagcbor.Decode(nb, buf)
		if err != nil {
			return nil, err
		}
		exts = append(exts, graphsync.ExtensionData{
			Name: supportedExtension,
			Data: nb.Build(),
		})
	}
	if len(exts) == 0 {
		return nil, errors.New("message not encodable in any supported extensions")
	}
	return exts, nil
}

// GsExtended is a small interface used by GetTransferData
type GsExtended interface {
	Extension(name graphsync.ExtensionName) (datamodel.Node, bool)
}

// GetTransferData unmarshals extension data.
// Returns:
//    * nil + nil if the extension is not found
//    * nil + error if the extendedData fails to unmarshal
//    * unmarshaled ExtensionDataTransferData + nil if all goes well
func GetTransferData(extendedData GsExtended, extNames []graphsync.ExtensionName) (datatransfer.Message, error) {
	for _, name := range extNames {
		data, ok := extendedData.Extension(name)
		if ok {
			buf := new(bytes.Buffer)
			err := dagcbor.Encode(data, buf)
			if err != nil {
				return nil, err
			}
			return decoders[name](buf)
		}
	}
	return nil, nil
}

type decoder func(io.Reader) (datatransfer.Message, error)

var decoders = map[graphsync.ExtensionName]decoder{
	ExtensionIncomingRequest1_1: message.FromNet,
	ExtensionOutgoingBlock1_1:   message.FromNet,
	ExtensionDataTransfer1_1:    message.FromNet,
}
