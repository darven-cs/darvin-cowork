package agent

import "errors"

// ErrAgentBusy is returned by Prompt when an Agent is already running.
// Use Steer to interrupt or FollowUp to queue for after the current run.
var ErrAgentBusy = errors.New("agent: busy, use Steer or FollowUp")

// ErrAborted is returned by Run when the run was aborted via Abort or a
// Steer that interrupted a previous run. Run returns this even if the
// underlying turn completed with stopReason=aborted; the Agent's last
// assistant message in the session is the partial turn output.
var ErrAborted = errors.New("agent: aborted")
