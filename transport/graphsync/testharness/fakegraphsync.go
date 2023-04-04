package testharness

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"testing"

	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-graphsync"
	"github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/datamodel"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/ipld/go-ipld-prime/traversal"
	selectorparse "github.com/ipld/go-ipld-prime/traversal/selector/parse"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/filecoin-project/go-data-transfer/v2/message"
	"github.com/filecoin-project/go-data-transfer/v2/testutil"
	"github.com/filecoin-project/go-data-transfer/v2/transport/graphsync/extension"
)

func matchDtMessage(t *testing.T, extensions []graphsync.ExtensionData, extName graphsync.ExtensionName) datatransfer.Message {
	var matchedExtension *graphsync.ExtensionData
	for _, ext := range extensions {
		if ext.Name == extName {
			matchedExtension = &ext
			break
		}
	}
	require.NotNil(t, matchedExtension)
	received, err := message.FromIPLD(matchedExtension.Data)
	require.NoError(t, err)
	return received
}

// ReceivedGraphSyncRequest contains data about a received graphsync request
type ReceivedGraphSyncRequest struct {
	Ctx             context.Context
	P               peer.ID
	Root            ipld.Link
	Selector        datamodel.Node
	Extensions      []graphsync.ExtensionData
	ResponseChan    chan graphsync.ResponseProgress
	ResponseErrChan chan error
}

func (gsRequest ReceivedGraphSyncRequest) ToRequestData(t *testing.T) graphsync.RequestData {
	extensions := make(map[graphsync.ExtensionName]datamodel.Node)
	for _, extension := range gsRequest.Extensions {
		extensions[extension.Name] = extension.Data
	}
	requestID, ok := gsRequest.requestID()
	require.True(t, ok)
	return NewFakeRequest(requestID, extensions, graphsync.RequestTypeNew)
}

func (gsRequest ReceivedGraphSyncRequest) Response(t *testing.T, incomingRequestMsg datatransfer.Message, blockMessage datatransfer.Message, code graphsync.ResponseStatusCode) graphsync.ResponseData {
	extensions := make(map[graphsync.ExtensionName]datamodel.Node)
	if incomingRequestMsg != nil {
		extensions[extension.ExtensionIncomingRequest1_1] = incomingRequestMsg.ToIPLD()
	}
	if blockMessage != nil {
		extensions[extension.ExtensionOutgoingBlock1_1] = blockMessage.ToIPLD()
	}
	requestID, ok := gsRequest.requestID()
	require.True(t, ok)
	return NewFakeResponse(requestID, extensions, code)
}

func (gsRequest ReceivedGraphSyncRequest) requestID() (graphsync.RequestID, bool) {
	request, ok := gsRequest.Ctx.Value(graphsync.RequestIDContextKey{}).(graphsync.RequestID)
	return request, ok
}

// DTMessage returns the data transfer message among the graphsync extensions sent with this request
func (gsRequest ReceivedGraphSyncRequest) DTMessage(t *testing.T) datatransfer.Message {
	return matchDtMessage(t, gsRequest.Extensions, extension.ExtensionDataTransfer1_1)
}

type Resume struct {
	RequestID  graphsync.RequestID
	Extensions []graphsync.ExtensionData
}

// DTMessage returns the data transfer message among the graphsync extensions sent with this request
func (resume Resume) DTMessage(t *testing.T) datatransfer.Message {
	return matchDtMessage(t, resume.Extensions, extension.ExtensionDataTransfer1_1)
}

type Update struct {
	RequestID  graphsync.RequestID
	Extensions []graphsync.ExtensionData
}

// DTMessage returns the data transfer message among the graphsync extensions sent with this request
func (update Update) DTMessage(t *testing.T) datatransfer.Message {
	return matchDtMessage(t, update.Extensions, extension.ExtensionDataTransfer1_1)
}

// FakeGraphSync implements a GraphExchange but does nothing
type FakeGraphSync struct {
	requests chan ReceivedGraphSyncRequest // records calls to fakeGraphSync.Request
	pauses   chan graphsync.RequestID
	resumes  chan Resume
	cancels  chan graphsync.RequestID
	updates  chan Update

	persistenceOptionsLk              sync.RWMutex
	persistenceOptions                map[string]ipld.LinkSystem
	leaveRequestsOpen                 bool
	OutgoingRequestHook               graphsync.OnOutgoingRequestHook
	IncomingBlockHook                 graphsync.OnIncomingBlockHook
	OutgoingBlockHook                 graphsync.OnOutgoingBlockHook
	IncomingRequestProcessingListener graphsync.OnRequestProcessingListener
	OutgoingRequestProcessingListener graphsync.OnRequestProcessingListener
	IncomingRequestHook               graphsync.OnIncomingRequestHook
	CompletedResponseListener         graphsync.OnResponseCompletedListener
	RequestUpdatedHook                graphsync.OnRequestUpdatedHook
	IncomingResponseHook              graphsync.OnIncomingResponseHook
	RequestorCancelledListener        graphsync.OnRequestorCancelledListener
	BlockSentListener                 graphsync.OnBlockSentListener
	NetworkErrorListener              graphsync.OnNetworkErrorListener
	ReceiverNetworkErrorListener      graphsync.OnReceiverNetworkErrorListener
	ReturnedCancelError               error
	ReturnedPauseError                error
	ReturnedResumeError               error
	ReturnedSendUpdateError           error
}

// NewFakeGraphSync returns a new fake graphsync implementation
func NewFakeGraphSync() *FakeGraphSync {
	return &FakeGraphSync{
		requests:           make(chan ReceivedGraphSyncRequest, 2),
		pauses:             make(chan graphsync.RequestID, 1),
		resumes:            make(chan Resume, 1),
		cancels:            make(chan graphsync.RequestID, 1),
		updates:            make(chan Update, 1),
		persistenceOptions: make(map[string]ipld.LinkSystem),
	}
}

func (fgs *FakeGraphSync) LeaveRequestsOpen() {
	fgs.leaveRequestsOpen = true
}

// AssertNoRequestReceived asserts that no requests should ahve been received by this graphsync implementation
func (fgs *FakeGraphSync) AssertNoRequestReceived(t *testing.T) {
	require.Empty(t, fgs.requests, "should not receive request")
}

// AssertRequestReceived asserts a request should be received before the context closes (and returns said request)
func (fgs *FakeGraphSync) AssertRequestReceived(ctx context.Context, t *testing.T) ReceivedGraphSyncRequest {
	var requestReceived ReceivedGraphSyncRequest
	select {
	case <-ctx.Done():
		t.Fatal("did not receive message sent")
	case requestReceived = <-fgs.requests:
	}
	return requestReceived
}

// AssertNoPauseReceived asserts that no pause requests should ahve been received by this graphsync implementation
func (fgs *FakeGraphSync) AssertNoPauseReceived(t *testing.T) {
	require.Empty(t, fgs.pauses, "should not receive pause request")
}

// AssertPauseReceived asserts a pause request should be received before the context closes (and returns said request)
func (fgs *FakeGraphSync) AssertPauseReceived(ctx context.Context, t *testing.T) graphsync.RequestID {
	var pauseReceived graphsync.RequestID
	select {
	case <-ctx.Done():
		t.Fatal("did not receive message sent")
	case pauseReceived = <-fgs.pauses:
	}
	return pauseReceived
}

// AssertNoResumeReceived asserts that no resume requests should ahve been received by this graphsync implementation
func (fgs *FakeGraphSync) AssertNoResumeReceived(t *testing.T) {
	require.Empty(t, fgs.resumes, "should not receive resume request")
}

// AssertResumeReceived asserts a resume request should be received before the context closes (and returns said request)
func (fgs *FakeGraphSync) AssertResumeReceived(ctx context.Context, t *testing.T) Resume {
	var resumeReceived Resume
	select {
	case <-ctx.Done():
		t.Fatal("did not receive message sent")
	case resumeReceived = <-fgs.resumes:
	}
	return resumeReceived
}

// AssertNoCancelReceived asserts that no requests were cancelled by thiss graphsync implementation
func (fgs *FakeGraphSync) AssertNoCancelReceived(t *testing.T) {
	require.Empty(t, fgs.cancels, "should not cancel request")
}

// AssertCancelReceived asserts a requests was cancelled before the context closes (and returns said request id)
func (fgs *FakeGraphSync) AssertCancelReceived(ctx context.Context, t *testing.T) graphsync.RequestID {
	var cancelReceived graphsync.RequestID
	select {
	case <-ctx.Done():
		t.Fatal("did not receive message sent")
	case cancelReceived = <-fgs.cancels:
	}
	return cancelReceived
}

// AssertHasPersistenceOption verifies that a persistence option was registered
func (fgs *FakeGraphSync) AssertHasPersistenceOption(t *testing.T, name string) ipld.LinkSystem {
	fgs.persistenceOptionsLk.RLock()
	defer fgs.persistenceOptionsLk.RUnlock()
	option, ok := fgs.persistenceOptions[name]
	require.Truef(t, ok, "persistence option %s should be registered", name)
	return option
}

// AssertDoesNotHavePersistenceOption verifies that a persistence option is not registered
func (fgs *FakeGraphSync) AssertDoesNotHavePersistenceOption(t *testing.T, name string) {
	fgs.persistenceOptionsLk.RLock()
	defer fgs.persistenceOptionsLk.RUnlock()
	_, ok := fgs.persistenceOptions[name]
	require.Falsef(t, ok, "persistence option %s should be registered", name)
}

// Request initiates a new GraphSync request to the given peer using the given selector spec.
func (fgs *FakeGraphSync) Request(ctx context.Context, p peer.ID, root ipld.Link, selector datamodel.Node, extensions ...graphsync.ExtensionData) (<-chan graphsync.ResponseProgress, <-chan error) {
	errors := make(chan error)
	responses := make(chan graphsync.ResponseProgress)
	fgs.requests <- ReceivedGraphSyncRequest{ctx, p, root, selector, extensions, responses, errors}
	if !fgs.leaveRequestsOpen {
		close(responses)
		close(errors)
	}
	return responses, errors
}

// RegisterPersistenceOption registers an alternate loader/storer combo that can be substituted for the default
func (fgs *FakeGraphSync) RegisterPersistenceOption(name string, lsys ipld.LinkSystem) error {
	fgs.persistenceOptionsLk.Lock()
	defer fgs.persistenceOptionsLk.Unlock()
	_, ok := fgs.persistenceOptions[name]
	if ok {
		return errors.New("already registered")
	}
	fgs.persistenceOptions[name] = lsys
	return nil
}

// UnregisterPersistenceOption unregisters an existing loader/storer combo
func (fgs *FakeGraphSync) UnregisterPersistenceOption(name string) error {
	fgs.persistenceOptionsLk.Lock()
	defer fgs.persistenceOptionsLk.Unlock()
	delete(fgs.persistenceOptions, name)
	return nil
}

// RegisterIncomingRequestHook adds a hook that runs when a request is received
func (fgs *FakeGraphSync) RegisterIncomingRequestHook(hook graphsync.OnIncomingRequestHook) graphsync.UnregisterHookFunc {
	fgs.IncomingRequestHook = hook
	return func() {
		fgs.IncomingRequestHook = nil
	}
}

// RegisterIncomingRequestProcessingListener adds a hook that runs when an incoming GS request begins processing
func (fgs *FakeGraphSync) RegisterIncomingRequestProcessingListener(hook graphsync.OnRequestProcessingListener) graphsync.UnregisterHookFunc {
	fgs.IncomingRequestProcessingListener = hook
	return func() {
		fgs.IncomingRequestProcessingListener = nil
	}
}

// RegisterIncomingResponseHook adds a hook that runs when a response is received
func (fgs *FakeGraphSync) RegisterIncomingResponseHook(hook graphsync.OnIncomingResponseHook) graphsync.UnregisterHookFunc {
	fgs.IncomingResponseHook = hook
	return func() {
		fgs.IncomingResponseHook = nil
	}
}

// RegisterOutgoingRequestHook adds a hook that runs immediately prior to sending a new request
func (fgs *FakeGraphSync) RegisterOutgoingRequestHook(hook graphsync.OnOutgoingRequestHook) graphsync.UnregisterHookFunc {
	fgs.OutgoingRequestHook = hook
	return func() {
		fgs.OutgoingRequestHook = nil
	}
}

// RegisterOutgoingBlockHook adds a hook that runs every time a block is sent from a responder
func (fgs *FakeGraphSync) RegisterOutgoingBlockHook(hook graphsync.OnOutgoingBlockHook) graphsync.UnregisterHookFunc {
	fgs.OutgoingBlockHook = hook
	return func() {
		fgs.OutgoingBlockHook = nil
	}
}

// RegisterIncomingBlockHook adds a hook that runs every time a block is received by the requestor
func (fgs *FakeGraphSync) RegisterIncomingBlockHook(hook graphsync.OnIncomingBlockHook) graphsync.UnregisterHookFunc {
	fgs.IncomingBlockHook = hook
	return func() {
		fgs.IncomingBlockHook = nil
	}
}

// RegisterRequestUpdatedHook adds a hook that runs every time an update to a request is received
func (fgs *FakeGraphSync) RegisterRequestUpdatedHook(hook graphsync.OnRequestUpdatedHook) graphsync.UnregisterHookFunc {
	fgs.RequestUpdatedHook = hook
	return func() {
		fgs.RequestUpdatedHook = nil
	}
}

// RegisterCompletedResponseListener adds a listener on the responder for completed responses
func (fgs *FakeGraphSync) RegisterCompletedResponseListener(listener graphsync.OnResponseCompletedListener) graphsync.UnregisterHookFunc {
	fgs.CompletedResponseListener = listener
	return func() {
		fgs.CompletedResponseListener = nil
	}
}

// Unpause unpauses a request that was paused in a block hook based on request ID
func (fgs *FakeGraphSync) Unpause(ctx context.Context, requestID graphsync.RequestID, extensions ...graphsync.ExtensionData) error {
	fgs.resumes <- Resume{requestID, extensions}
	return fgs.ReturnedResumeError
}

// Pause pauses a request based on request ID
func (fgs *FakeGraphSync) Pause(ctx context.Context, requestID graphsync.RequestID) error {
	fgs.pauses <- requestID
	return fgs.ReturnedPauseError
}

func (fgs *FakeGraphSync) Cancel(ctx context.Context, requestID graphsync.RequestID) error {
	fgs.cancels <- requestID
	return fgs.ReturnedCancelError
}

// RegisterRequestorCancelledListener adds a listener on the responder for requests cancelled by the requestor
func (fgs *FakeGraphSync) RegisterRequestorCancelledListener(listener graphsync.OnRequestorCancelledListener) graphsync.UnregisterHookFunc {
	fgs.RequestorCancelledListener = listener
	return func() {
		fgs.RequestorCancelledListener = nil
	}
}

// RegisterBlockSentListener adds a listener on the responder as blocks go out
func (fgs *FakeGraphSync) RegisterBlockSentListener(listener graphsync.OnBlockSentListener) graphsync.UnregisterHookFunc {
	fgs.BlockSentListener = listener
	return func() {
		fgs.BlockSentListener = nil
	}
}

// RegisterNetworkErrorListener adds a listener on the responder as blocks go out
func (fgs *FakeGraphSync) RegisterNetworkErrorListener(listener graphsync.OnNetworkErrorListener) graphsync.UnregisterHookFunc {
	fgs.NetworkErrorListener = listener
	return func() {
		fgs.NetworkErrorListener = nil
	}
}

// RegisterNetworkErrorListener adds a listener on the responder as blocks go out
func (fgs *FakeGraphSync) RegisterReceiverNetworkErrorListener(listener graphsync.OnReceiverNetworkErrorListener) graphsync.UnregisterHookFunc {
	fgs.ReceiverNetworkErrorListener = listener
	return func() {
		fgs.ReceiverNetworkErrorListener = nil
	}
}

func (fgs *FakeGraphSync) Stats() graphsync.Stats {
	return graphsync.Stats{}
}

func (fgs *FakeGraphSync) RegisterOutgoingRequestProcessingListener(listener graphsync.OnRequestProcessingListener) graphsync.UnregisterHookFunc {
	fgs.OutgoingRequestProcessingListener = listener
	return func() {
		fgs.OutgoingRequestProcessingListener = nil
	}
}

func (fgs *FakeGraphSync) SendUpdate(ctx context.Context, id graphsync.RequestID, extensions ...graphsync.ExtensionData) error {
	fgs.updates <- Update{RequestID: id, Extensions: extensions}
	return fgs.ReturnedSendUpdateError
}

var _ graphsync.GraphExchange = &FakeGraphSync{}

type fakeBlkData struct {
	link   ipld.Link
	size   uint64
	onWire bool
	index  int64
}

func (fbd fakeBlkData) Link() ipld.Link {
	return fbd.link
}

func (fbd fakeBlkData) BlockSize() uint64 {
	return fbd.size
}

func (fbd fakeBlkData) BlockSizeOnWire() uint64 {
	if fbd.onWire {
		return fbd.size
	}
	return 0
}

func (fbd fakeBlkData) Index() int64 {
	return fbd.index
}

// NewFakeBlockData returns a fake block that matches the block data interface
func NewFakeBlockData(size uint64, index int64, onWire bool) graphsync.BlockData {
	return &fakeBlkData{
		link:   cidlink.Link{Cid: testutil.GenerateCids(1)[0]},
		size:   size,
		index:  index,
		onWire: onWire,
	}
}

type fakeRequest struct {
	id          graphsync.RequestID
	root        cid.Cid
	selector    datamodel.Node
	priority    graphsync.Priority
	requestType graphsync.RequestType
	extensions  map[graphsync.ExtensionName]datamodel.Node
}

// ID Returns the request ID for this Request
func (fr *fakeRequest) ID() graphsync.RequestID {
	return fr.id
}

// Root returns the CID to the root block of this request
func (fr *fakeRequest) Root() cid.Cid {
	return fr.root
}

// Selector returns the byte representation of the selector for this request
func (fr *fakeRequest) Selector() datamodel.Node {
	return fr.selector
}

// Priority returns the priority of this request
func (fr *fakeRequest) Priority() graphsync.Priority {
	return fr.priority
}

// Extension returns the content for an extension on a response, or errors
// if extension is not present
func (fr *fakeRequest) Extension(name graphsync.ExtensionName) (datamodel.Node, bool) {
	data, has := fr.extensions[name]
	return data, has
}

// Type returns the type of request
func (fr *fakeRequest) Type() graphsync.RequestType {
	return fr.requestType
}

// NewFakeRequest returns a fake request that matches the request data interface
func NewFakeRequest(id graphsync.RequestID, extensions map[graphsync.ExtensionName]datamodel.Node, requestType graphsync.RequestType) graphsync.RequestData {
	return &fakeRequest{
		id:          id,
		root:        testutil.GenerateCids(1)[0],
		selector:    selectorparse.CommonSelector_ExploreAllRecursively,
		priority:    graphsync.Priority(rand.Int()),
		extensions:  extensions,
		requestType: requestType,
	}
}

type fakeResponse struct {
	id         graphsync.RequestID
	status     graphsync.ResponseStatusCode
	extensions map[graphsync.ExtensionName]datamodel.Node
}

// RequestID returns the request ID for this response
func (fr *fakeResponse) RequestID() graphsync.RequestID {
	return fr.id
}

// Status returns the status for a response
func (fr *fakeResponse) Status() graphsync.ResponseStatusCode {
	return fr.status
}

// Extension returns the content for an extension on a response, or errors
// if extension is not present
func (fr *fakeResponse) Extension(name graphsync.ExtensionName) (datamodel.Node, bool) {
	data, has := fr.extensions[name]
	return data, has
}

// Metadata returns metadata for this response
func (fr *fakeResponse) Metadata() graphsync.LinkMetadata {
	return nil
}

// NewFakeResponse returns a fake response that matches the response data interface
func NewFakeResponse(id graphsync.RequestID, extensions map[graphsync.ExtensionName]datamodel.Node, status graphsync.ResponseStatusCode) graphsync.ResponseData {
	return &fakeResponse{
		id:         id,
		status:     status,
		extensions: extensions,
	}
}

type FakeOutgoingRequestHookActions struct {
	PersistenceOption string
	MaxLinksOption    uint64
}

func (fa *FakeOutgoingRequestHookActions) UsePersistenceOption(name string) {
	fa.PersistenceOption = name
}
func (fa *FakeOutgoingRequestHookActions) UseLinkTargetNodePrototypeChooser(_ traversal.LinkTargetNodePrototypeChooser) {
}

func (fa *FakeOutgoingRequestHookActions) MaxLinks(maxLinks uint64) {
	fa.MaxLinksOption = maxLinks
}

var _ graphsync.OutgoingRequestHookActions = &FakeOutgoingRequestHookActions{}

type FakeIncomingBlockHookActions struct {
	TerminationError error
	SentExtensions   []graphsync.ExtensionData
	Paused           bool
}

func (fa *FakeIncomingBlockHookActions) TerminateWithError(err error) {
	fa.TerminationError = err
}

func (fa *FakeIncomingBlockHookActions) UpdateRequestWithExtensions(extensions ...graphsync.ExtensionData) {
	fa.SentExtensions = append(fa.SentExtensions, extensions...)
}

func (fa *FakeIncomingBlockHookActions) PauseRequest() {
	fa.Paused = true
}

var _ graphsync.IncomingBlockHookActions = &FakeIncomingBlockHookActions{}

type FakeOutgoingBlockHookActions struct {
	TerminationError error
	SentExtensions   []graphsync.ExtensionData
	Paused           bool
}

func (fa *FakeOutgoingBlockHookActions) SendExtensionData(extension graphsync.ExtensionData) {
	fa.SentExtensions = append(fa.SentExtensions, extension)
}

func (fa *FakeOutgoingBlockHookActions) TerminateWithError(err error) {
	fa.TerminationError = err
}

func (fa *FakeOutgoingBlockHookActions) PauseResponse() {
	fa.Paused = true
}

var _ graphsync.OutgoingBlockHookActions = &FakeOutgoingBlockHookActions{}

type FakeIncomingRequestHookActions struct {
	PersistenceOption string
	TerminationError  error
	Validated         bool
	SentExtensions    []graphsync.ExtensionData
	Paused            bool
	CtxAugFuncs       []func(context.Context) context.Context
	MaxLinksOption    uint64
}

func (fa *FakeIncomingRequestHookActions) SendExtensionData(ext graphsync.ExtensionData) {
	fa.SentExtensions = append(fa.SentExtensions, ext)
}

func (fa *FakeIncomingRequestHookActions) UsePersistenceOption(name string) {
	fa.PersistenceOption = name
}

func (fa *FakeIncomingRequestHookActions) UseLinkTargetNodePrototypeChooser(_ traversal.LinkTargetNodePrototypeChooser) {
}

func (fa *FakeIncomingRequestHookActions) TerminateWithError(err error) {
	fa.TerminationError = err
}

func (fa *FakeIncomingRequestHookActions) ValidateRequest() {
	fa.Validated = true
}

func (fa *FakeIncomingRequestHookActions) PauseResponse() {
	fa.Paused = true
}

func (fa *FakeIncomingRequestHookActions) AugmentContext(ctxAugFunc func(reqCtx context.Context) context.Context) {
	fa.CtxAugFuncs = append(fa.CtxAugFuncs, ctxAugFunc)
}

func (fa *FakeIncomingRequestHookActions) AssertAugmentedContextKey(t *testing.T, key interface{}, value interface{}) {
	ctx := context.Background()
	for _, f := range fa.CtxAugFuncs {
		ctx = f(ctx)
	}
	require.Equal(t, value, ctx.Value(key))
}

func (fa *FakeIncomingRequestHookActions) RefuteAugmentedContextKey(t *testing.T, key interface{}) {
	ctx := context.Background()
	for _, f := range fa.CtxAugFuncs {
		ctx = f(ctx)
	}
	require.Nil(t, ctx.Value(key))
}

func (fa *FakeIncomingRequestHookActions) DTMessage(t *testing.T) datatransfer.Message {
	return matchDtMessage(t, fa.SentExtensions, extension.ExtensionIncomingRequest1_1)
}

func (fa *FakeIncomingRequestHookActions) MaxLinks(maxLinks uint64) {
	fa.MaxLinksOption = maxLinks
}

var _ graphsync.IncomingRequestHookActions = &FakeIncomingRequestHookActions{}

type FakeRequestUpdatedActions struct {
	TerminationError error
	SentExtensions   []graphsync.ExtensionData
	Unpaused         bool
}

func (fa *FakeRequestUpdatedActions) SendExtensionData(extension graphsync.ExtensionData) {
	fa.SentExtensions = append(fa.SentExtensions, extension)
}

func (fa *FakeRequestUpdatedActions) TerminateWithError(err error) {
	fa.TerminationError = err
}

func (fa *FakeRequestUpdatedActions) UnpauseResponse() {
	fa.Unpaused = true
}

var _ graphsync.RequestUpdatedHookActions = &FakeRequestUpdatedActions{}

type FakeIncomingResponseHookActions struct {
	TerminationError error
	SentExtensions   []graphsync.ExtensionData
}

func (fa *FakeIncomingResponseHookActions) TerminateWithError(err error) {
	fa.TerminationError = err
}

func (fa *FakeIncomingResponseHookActions) UpdateRequestWithExtensions(extensions ...graphsync.ExtensionData) {
	fa.SentExtensions = append(fa.SentExtensions, extensions...)
}

var _ graphsync.IncomingResponseHookActions = &FakeIncomingResponseHookActions{}
