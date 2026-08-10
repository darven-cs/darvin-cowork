// Subagent types and the SubagentsAccessor interface that *Agent owns.
// Kept in protocol so neither internal/subagent (which implements this)
// nor internal/agents (which holds an accessor field) needs to import
// each other — the factory wires the implementation in.

package protocol

import (
	"context"
	"time"
)

// SubagentStatus is the lifecycle of a single sub-agent run.
type SubagentStatus string

const (
	SubagentPending SubagentStatus = "pending"
	SubagentRunning SubagentStatus = "running"
	SubagentDone    SubagentStatus = "done"
	SubagentError   SubagentStatus = "error"
	SubagentAborted SubagentStatus = "aborted"
	SubagentTimeout SubagentStatus = "timeout"
)

// SubagentSpec is the input to SubagentsAccessor.Spawn. Scope is the
// tool-name whitelist; an empty slice picks the default read-only set.
// RunInBackground releases Spawn immediately; Wait collects later.
type SubagentSpec struct {
	Prompt          string
	Description     string
	Scope           []string
	Model           string
	RunInBackground bool
	TimeoutMs       int
	ToolCallID      string
}

// SubagentInfo is the public, JSON-marshallable view of a run. The
// renderer reads this from the Subagents special tab in the artifact
// panel.
type SubagentInfo struct {
	ID              string         `json:"id"`
	ParentID        string         `json:"parentId"`
	Status          SubagentStatus `json:"status"`
	Prompt          string         `json:"prompt"`
	Description     string         `json:"description"`
	Scope           []string       `json:"scope"`
	Model           string         `json:"model"`
	ToolCallID      string         `json:"toolCallId,omitempty"`
	StartedAt       int64          `json:"startedAt"` // unix ms
	EndedAt         int64          `json:"endedAt"`   // unix ms; 0 if not ended
	ToolCalls       int            `json:"toolCalls"`
	ErrorMsg        string         `json:"errorMsg,omitempty"`
	DurationMs      int64          `json:"durationMs"`
	ResultText      string         `json:"resultText,omitempty"`
	ResultTruncated bool           `json:"resultTruncated,omitempty"`
}

// SubagentsAccessor is the surface Agent and AgentLoopSession consume.
// Implementations live in internal/subagent/.
type SubagentsAccessor interface {
	Spawn(ctx context.Context, spec SubagentSpec) (*SubagentInfo, error)
	List() []SubagentInfo
	Get(id string) (SubagentInfo, error)
	Abort(id string) error
	ReadResult(id string, offset, limit int) (string, error)
	Wait(id string, timeout time.Duration) (SubagentInfo, error)
	Close()
}

// IsTerminal reports whether a status is final (no further state change
// expected). SubagentPending and SubagentRunning are non-terminal.
func (s SubagentStatus) IsTerminal() bool {
	switch s {
	case SubagentDone, SubagentError, SubagentAborted, SubagentTimeout:
		return true
	}
	return false
}
