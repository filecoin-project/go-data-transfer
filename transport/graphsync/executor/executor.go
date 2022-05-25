package executor

import (
	"fmt"

	datatransfer "github.com/filecoin-project/go-data-transfer/v2"
	"github.com/ipfs/go-graphsync"
	logging "github.com/ipfs/go-log/v2"
)

var log = logging.Logger("dt_graphsync")

// EventsHandler are the data transfer events that can be dispatched by the execetor
type EventsHandler interface {
	OnRequestCancelled(datatransfer.ChannelID, error) error
	OnChannelCompleted(datatransfer.ChannelID, error) error
}

// Executor handles consuming channels on an outgoing GraphSync request
type Executor struct {
	channelID    datatransfer.ChannelID
	responseChan <-chan graphsync.ResponseProgress
	errChan      <-chan error
	onComplete   func()
}

// NewExecutor sets up a new executor to consume a graphsync request
func NewExecutor(
	channelID datatransfer.ChannelID,
	responseChan <-chan graphsync.ResponseProgress,
	errChan <-chan error,
	onComplete func()) *Executor {
	return &Executor{channelID, responseChan, errChan, onComplete}
}

// Start initiates consumption of a graphsync request
func (e *Executor) Start(events EventsHandler,
	completedRequestListener func(channelID datatransfer.ChannelID)) {
	go e.executeRequest(events, completedRequestListener)
}

// Read from the graphsync response and error channels until they are closed,
// and return the last error on the error channel
func (e *Executor) consumeResponses() error {
	var lastError error
	for range e.responseChan {
	}
	log.Infof("channel %s: finished consuming graphsync response channel", e.channelID)

	for err := range e.errChan {
		lastError = err
	}
	log.Infof("channel %s: finished consuming graphsync error channel", e.channelID)

	return lastError
}

// Read from the graphsync response and error channels until they are closed
// or there is an error, then call the channel completed callback
func (e *Executor) executeRequest(
	events EventsHandler,
	completedRequestListener func(channelID datatransfer.ChannelID)) {
	// Make sure to call the onComplete callback before returning
	defer func() {
		log.Infow("gs request complete for channel", "chid", e.channelID)
		e.onComplete()
	}()

	// Consume the response and error channels for the graphsync request
	lastError := e.consumeResponses()

	// Request cancelled by client
	if _, ok := lastError.(graphsync.RequestClientCancelledErr); ok {
		terr := fmt.Errorf("graphsync request cancelled")
		log.Warnf("channel %s: %s", e.channelID, terr)
		if err := events.OnRequestCancelled(e.channelID, terr); err != nil {
			log.Error(err)
		}
		return
	}

	// Request cancelled by responder
	if _, ok := lastError.(graphsync.RequestCancelledErr); ok {
		log.Infof("channel %s: graphsync request cancelled by responder", e.channelID)
		// TODO Should we do anything for RequestCancelledErr ?
		return
	}

	if lastError != nil {
		log.Warnf("channel %s: graphsync error: %s", e.channelID, lastError)
	}

	log.Debugf("channel %s: finished executing graphsync request", e.channelID)

	var completeErr error
	if lastError != nil {
		completeErr = fmt.Errorf("channel %s: graphsync request failed to complete: %w", e.channelID, lastError)
	}

	// Used by the tests to listen for when a request completes
	if completedRequestListener != nil {
		completedRequestListener(e.channelID)
	}
	err := events.OnChannelCompleted(e.channelID, completeErr)
	if err != nil {
		log.Errorf("channel %s: processing OnChannelCompleted: %s", e.channelID, err)
	}
}
