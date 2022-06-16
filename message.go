package datatransfer

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/ipfs/go-cid"
	"github.com/ipld/go-ipld-prime/datamodel"
)

type Version struct {
	Major uint64
	Minor uint64
	Patch uint64
}

func (mv Version) String() string {
	return fmt.Sprintf("%d.%d.%d", mv.Major, mv.Minor, mv.Patch)
}

// MessageVersionFromString parses a string into a message version
func MessageVersionFromString(versionString string) (Version, error) {
	versions := strings.Split(versionString, ".")
	if len(versions) != 3 {
		return Version{}, errors.New("not a version string")
	}
	major, err := strconv.ParseUint(versions[0], 10, 0)
	if err != nil {
		return Version{}, errors.New("unable to parse major version")
	}
	minor, err := strconv.ParseUint(versions[1], 10, 0)
	if err != nil {
		return Version{}, errors.New("unable to parse major version")
	}
	patch, err := strconv.ParseUint(versions[2], 10, 0)
	if err != nil {
		return Version{}, errors.New("unable to parse major version")
	}
	return Version{Major: major, Minor: minor, Patch: patch}, nil
}

var (
	// DataTransfer1_2 is the identifier for the current
	// supported version of data-transfer
	DataTransfer1_2 Version = Version{1, 2, 0}
)

// Message is a message for the data transfer protocol
// (either request or response) that can serialize to a protobuf
type Message interface {
	IsRequest() bool
	IsRestart() bool
	IsNew() bool
	IsUpdate() bool
	IsPaused() bool
	IsCancel() bool
	TransferID() TransferID
	ToNet(w io.Writer) error
	ToIPLD() datamodel.Node
	MessageForVersion(targetProtocol Version) (newMsg Message, err error)
	Version() Version
	WrappedForTransport(transportID TransportID, transportVersion Version) TransportedMessage
}

// TransportedMessage is a message that can also report how it was transported
type TransportedMessage interface {
	Message
	TransportID() TransportID
	TransportVersion() Version
}

// Request is a response message for the data transfer protocol
type Request interface {
	Message
	IsPull() bool
	IsVoucher() bool
	VoucherType() TypeIdentifier
	Voucher() (datamodel.Node, error)
	TypedVoucher() (TypedVoucher, error)
	BaseCid() cid.Cid
	Selector() (datamodel.Node, error)
	IsRestartExistingChannelRequest() bool
	RestartChannelId() (ChannelID, error)
}

// Response is a response message for the data transfer protocol
type Response interface {
	Message
	IsValidationResult() bool
	IsComplete() bool
	Accepted() bool
	VoucherResultType() TypeIdentifier
	VoucherResult() (datamodel.Node, error)
	EmptyVoucherResult() bool
}
