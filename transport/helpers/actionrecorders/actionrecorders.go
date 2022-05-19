package actionrecorders

import datatransfer "github.com/filecoin-project/go-data-transfer/v2"

// ChannelActionsRecorder implements datatransfer.ChannelActions by just r
// recording method calls so they can be processed in a deferred manner
type ChannelActionsRecorder struct {
	DoClose     bool
	MessageSent datatransfer.Message
}

// CloseChannel close this channel and effectively closes it to further
// action
func (car *ChannelActionsRecorder) CloseChannel() {
	car.DoClose = true
}

// SendMessage sends an arbitrary message over a transport
func (car *ChannelActionsRecorder) SendMessage(message datatransfer.Message) {
	car.MessageSent = message
}

// PauseActionsRecorder implements datatransfer.PauseActions by just
// recording method calls so they can be processed in a deferred manner
type PauseActionsRecorder struct {
	DoPause  bool
	DoResume bool
}

// PauseChannel paused the given channel ID
func (par *PauseActionsRecorder) PauseChannel() {
	par.DoPause = true
}

// ResumeChannel resumes the given channel
func (par *PauseActionsRecorder) ResumeChannel() {
	par.DoResume = true
}
