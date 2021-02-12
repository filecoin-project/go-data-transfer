package datatransfer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-core/test"
	"github.com/stretchr/testify/require"
)

func TestRegularChannelIDCBORMarshal(t *testing.T) {
	initiator := test.RandPeerIDFatal(t)
	responder := test.RandPeerIDFatal(t)
	tid := TransferID(0)

	chid := ChannelID{
		Initiator: initiator,
		Responder: responder,
		ID:        tid,
	}
	buf := new(bytes.Buffer)
	err := chid.MarshalCBOR(buf)
	require.NoError(t, err)

	unm := ChannelID{}
	err = unm.UnmarshalCBOR(buf)
	require.NoError(t, err)

	require.Equal(t, initiator, unm.Initiator)
	require.Equal(t, responder, unm.Responder)
	require.Equal(t, tid, unm.ID)
	require.Equal(t, fmt.Sprintf("%s-%s-%d", initiator, responder, tid), unm.String())
}

func TestUndefinedChannelIDCBORMarshal(t *testing.T) {
	chid := ChannelID{}
	buf := new(bytes.Buffer)
	err := chid.MarshalCBOR(buf)
	require.NoError(t, err)

	unm := ChannelID{}
	err = unm.UnmarshalCBOR(buf)
	require.NoError(t, err)

	require.Equal(t, peer.ID(""), unm.Responder)
	require.Equal(t, peer.ID(""), unm.Initiator)
	require.Equal(t, TransferID(0), unm.ID)

	require.Equal(t, "Undefined data-transfer ChannelID", unm.String())
}

func TestRegularChannelIDJSONMarshal(t *testing.T) {
	initiator := test.RandPeerIDFatal(t)
	responder := test.RandPeerIDFatal(t)
	tid := TransferID(0)

	chid := ChannelID{
		Initiator: initiator,
		Responder: responder,
		ID:        tid,
	}
	chidjson, err := json.Marshal(chid)
	require.NoError(t, err)

	unm := ChannelID{}
	err = json.Unmarshal(chidjson, &unm)
	require.NoError(t, err)

	require.Equal(t, initiator, unm.Initiator)
	require.Equal(t, responder, unm.Responder)
	require.Equal(t, tid, unm.ID)
	require.Equal(t, fmt.Sprintf("%s-%s-%d", initiator, responder, tid), unm.String())
}

func TestUndefinedChannelIDJSONMarshal(t *testing.T) {
	chid := ChannelID{}
	chidjson, err := json.Marshal(chid)
	require.NoError(t, err)

	unm := ChannelID{}
	err = json.Unmarshal(chidjson, &unm)
	require.NoError(t, err)

	require.Equal(t, peer.ID(""), unm.Responder)
	require.Equal(t, peer.ID(""), unm.Initiator)
	require.Equal(t, TransferID(0), unm.ID)

	require.Equal(t, "Undefined data-transfer ChannelID", unm.String())
}
