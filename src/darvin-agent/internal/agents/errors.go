// Sentinel errors returned across the agent run / prompt lifecycle.

package agent

import "errors"

// ErrAgentBusy is returned by Prompt when an Agent is already running.
// Callers should Abort first then Prompt.
var ErrAgentBusy = errors.New("agent: busy, call Abort first")

// ErrAborted is returned by Run when the run was aborted via Abort or
// the ctx it was started with fired. Run returns this even if the
// underlying turn completed with stopReason=aborted; the Agent's last
// assistant message in the session is the partial turn output.
var ErrAborted = errors.New("agent: aborted")
