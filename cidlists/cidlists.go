package cidlists

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	cbg "github.com/whyrusleeping/cbor-gen"

	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/ipfs/go-cid"
)

// CIDLists maintains files that contain a list of CIDs received for different data transfers
type CIDLists struct {
	baseDir string
}

// NewCIDLists initializes a new set of cid lists in a given directory
func NewCIDLists(baseDir string) (*CIDLists, error) {
	base := filepath.Clean(string(baseDir))
	info, err := os.Stat(string(base))
	if err != nil {
		return nil, fmt.Errorf("error getting %s info: %s", base, err.Error())
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", base)
	}
	return &CIDLists{
		baseDir: base,
	}, nil
}

// CreateList initializes a new CID list with the given initial cids (or can be empty) for a data transfer channel
func (cl *CIDLists) CreateList(chid datatransfer.ChannelID, initalCids []cid.Cid) error {
	f, err := os.Create(transferFilename(cl.baseDir, chid))
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
	}()
	for _, c := range initalCids {
		err := cbg.WriteCid(f, c)
		if err != nil {
			return err
		}
	}
	return nil
}

// AppendList appends a single CID to the list for a given data transfer channel
func (cl *CIDLists) AppendList(chid datatransfer.ChannelID, c cid.Cid) error {
	f, err := os.OpenFile(transferFilename(cl.baseDir, chid), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
	}()
	return cbg.WriteCid(f, c)
}

// ReadList reads an on disk list of cids for the given data transfer channel
func (cl *CIDLists) ReadList(chid datatransfer.ChannelID) ([]cid.Cid, error) {
	f, err := os.Open(transferFilename(cl.baseDir, chid))
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = f.Close()
	}()
	var receivedCids []cid.Cid
	for {
		c, err := cbg.ReadCid(f)
		if err != nil {
			if err == io.EOF {
				return receivedCids, nil
			}
			return nil, err
		}
		receivedCids = append(receivedCids, c)
	}
}

// DeleteList deletes the lsit for the given data transfer channel
func (cl *CIDLists) DeleteList(chid datatransfer.ChannelID) error {
	return os.Remove(transferFilename(cl.baseDir, chid))
}

func transferFilename(baseDir string, chid datatransfer.ChannelID) string {
	filename := fmt.Sprintf("%d-%s-%s", chid.ID, chid.Initiator, chid.Responder)
	return filepath.Join(baseDir, filename)
}
