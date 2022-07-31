package datatransfer

type errorType string

func (e errorType) Error() string {
	return string(e)
}

// ErrHandlerAlreadySet means an event handler was already set for this instance of
// hooks
const ErrHandlerAlreadySet = errorType("already set event handler")

// ErrHandlerNotSet means you cannot issue commands to this interface because the
// handler has not been set
const ErrHandlerNotSet = errorType("event handler has not been set")

// ErrChannelNotFound means the channel this command was issued for does not exist
const ErrChannelNotFound = errorType("channel not found")

// ErrRejected indicates a request was not accepted
const ErrRejected = errorType("response rejected")

// ErrUnsupported indicates an operation is not supported by the transport protocol
const ErrUnsupported = errorType("unsupported")
