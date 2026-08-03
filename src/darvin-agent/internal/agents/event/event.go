// Package event defines the agent lifecycle event protocol and a fan-out bus
// used by the Agent to publish events to multiple subscribers.
//
// Events are sealed-sum types (interface + unexported marker). Subscribers
// register via Bus.Subscribe; events are dropped (oldest first) on a full
// channel to keep the agent's main loop non-blocking.
//
// EventCommon is the correlation payload (sessionID + messageID) embedded
// in every concrete event; consumers (EventLedger, dispatcher, executor)
// read it via the Event.Common() method so per-session routing doesn't
// need a type switch.
package event

import (
	"sync"

	"darvin-cowork/backend/internal/agents/protocol"
)

// Mode mirrors the queue's PromptMode so PromptReceivedEvent can carry the
// origin of the message without depending on the queue package.
type Mode string

const (
	ModePrompt   Mode = "prompt"
	ModeSteer    Mode = "steer"
	ModeFollowUp Mode = "followup"
)

// EventCommon is the correlation payload embedded by every concrete Event.
// Consumers read it through Event.Common() — no type switch needed.
type EventCommon struct {
	SessionID string
	RunID     string
	MessageID string
}

// EventBase provides the Event.Common() method for every concrete Event
// that embeds it. One-line embed on the event side; consumers see
// Common() uniformly via the Event interface. The struct is exported so
// callers can construct events with explicit EventCommon: event.EventBase{
// EventCommon: ... } literal syntax; the Common() method itself stays
// the same.
type EventBase struct{ EventCommon }

func (b EventBase) Common() EventCommon { return b.EventCommon }

// Event is the sealed agent lifecycle event. All concrete event types
// implement the unexported isAgentEvent marker.
type Event interface {
	isAgentEvent()
	EventName() string
	Common() EventCommon
}

// ToolResult is the event-level view of a tool execution outcome. Defined
// here (rather than imported from package tool) to keep event independent
// of the tool package and to avoid an import cycle.
type ToolResult struct {
	Content  string
	IsError  bool
	Metadata map[string]any
}

// PromptReceivedEvent is emitted when Agent.Prompt / Steer / FollowUp accepts
// a message into its queue.
type PromptReceivedEvent struct {
	EventBase
	Content string
	Mode    Mode
}

func (PromptReceivedEvent) isAgentEvent()     {}
func (PromptReceivedEvent) EventName() string { return "prompt_received" }

// RunStartEvent marks the start of a single Run (one or more turns serving
// the next dequeued message).
type RunStartEvent struct {
	EventBase
}

func (RunStartEvent) isAgentEvent()     {}
func (RunStartEvent) EventName() string { return "run_start" }

// TurnStartEvent marks the beginning of one LLM round-trip.
type TurnStartEvent struct {
	EventBase
	TurnID    string
	TurnIndex int
}

func (TurnStartEvent) isAgentEvent()     {}
func (TurnStartEvent) EventName() string { return "turn_start" }

// LLMStartEvent is emitted right before provider.Stream is called.
type LLMStartEvent struct {
	EventBase
	Model string
}

func (LLMStartEvent) isAgentEvent()     {}
func (LLMStartEvent) EventName() string { return "llm_start" }

// TextDeltaEvent is a passthrough of provider.TextDeltaEvent.
type TextDeltaEvent struct {
	EventBase
	Delta string
}

func (TextDeltaEvent) isAgentEvent()     {}
func (TextDeltaEvent) EventName() string { return "text_delta" }

// ThinkingDeltaEvent is a passthrough of provider.ThinkingDeltaEvent —
// an incremental chunk of the model's extended-thinking output.
type ThinkingDeltaEvent struct {
	EventBase
	Delta string
}

func (ThinkingDeltaEvent) isAgentEvent()     {}
func (ThinkingDeltaEvent) EventName() string { return "thinking_delta" }

// LLMEndEvent fires after the provider's stream closes (DoneEvent or
// ErrorEvent), with the accumulated assistant message and usage.
type LLMEndEvent struct {
	EventBase
	Assistant protocol.Message
	Usage     protocol.Usage
}

func (LLMEndEvent) isAgentEvent()     {}
func (LLMEndEvent) EventName() string { return "llm_end" }

// ToolStartEvent is emitted before a tool is invoked. One per call when the
// assistant issued multiple tool calls in a single turn. ToolKind is empty
// for built-ins, "skill" / "mcp" otherwise; SkillID and McpServerID are set
// for the corresponding kind.
type ToolStartEvent struct {
	EventBase
	TurnID      string
	CallID      string
	Name        string
	ToolKind    string
	SkillID     string
	McpServerID string
	Arguments   map[string]any
}

func (ToolStartEvent) isAgentEvent()     {}
func (ToolStartEvent) EventName() string { return "tool_start" }

// ToolEndEvent fires after a tool's Execute returns. Duration is measured
// inside the executor goroutine.
type ToolEndEvent struct {
	EventBase
	CallID      string
	ToolKind    string
	SkillID     string
	McpServerID string
	Result      ToolResult
	DurationMS  int64
}

func (ToolEndEvent) isAgentEvent()     {}
func (ToolEndEvent) EventName() string { return "tool_end" }

// TurnEndEvent closes a single turn.
type TurnEndEvent struct {
	EventBase
	TurnIndex  int
	StopReason protocol.FinishReason
}

func (TurnEndEvent) isAgentEvent()     {}
func (TurnEndEvent) EventName() string { return "turn_end" }

// RunEndEvent marks the end of a Run (all turns for one dequeued message).
type RunEndEvent struct {
	EventBase
	Turns int
}

func (RunEndEvent) isAgentEvent()     {}
func (RunEndEvent) EventName() string { return "run_end" }

// AgentErrorEvent signals a non-fatal error. The Agent may still produce
// further events; a terminal abort is signalled by FinishReasonAborted
// in the corresponding TurnEndEvent / AgentEndEvent.
type AgentErrorEvent struct {
	EventBase
	Err error
}

func (AgentErrorEvent) isAgentEvent()     {}
func (AgentErrorEvent) EventName() string { return "agent_error" }

// AgentEndEvent marks the very end of Agent.Run. After this, the Agent
// returns to idle and may accept new prompts (or auto-drain FollowUp).
type AgentEndEvent struct {
	EventBase
	TotalTurns int
	TotalUsage protocol.Usage
}

func (AgentEndEvent) isAgentEvent()     {}
func (AgentEndEvent) EventName() string { return "agent_end" }

// PermissionRequestEvent is emitted before a tool that needs user approval
// runs. The renderer shows a modal and answers via the
// agent.permission_response RPC (keyed by RequestID).
type PermissionRequestEvent struct {
	EventBase
	RequestID   string
	ToolName    string
	ToolInput   map[string]any
	DangerLevel string // safe | caution | destructive
	Reason      string
}

func (PermissionRequestEvent) isAgentEvent()     {}
func (PermissionRequestEvent) EventName() string { return "permission_request" }

// CompactionEvent signals a context compaction. The agent loop does not
// emit it directly; the ContextEngine produces it when Compact() runs.
type CompactionEvent struct {
	EventBase
	Before int
	After  int
	Note   string
}

func (CompactionEvent) isAgentEvent()     {}
func (CompactionEvent) EventName() string { return "compaction" }

// ContextUsageEvent reports the session's context occupancy after a run
// completes; the renderer drives the context ring from this snapshot.
type ContextUsageEvent struct {
	EventBase
	UsedTokens    int
	ContextTokens int
	Percent       int
}

func (ContextUsageEvent) isAgentEvent()     {}
func (ContextUsageEvent) EventName() string { return "context_usage" }

// CustomEvent is an out-of-band channel for domain-specific events
// (Skills / MCP / etc.) to publish without expanding the agent core.
type CustomEvent struct {
	EventBase
	Name    string
	Payload any
}

func (CustomEvent) isAgentEvent() {}
func (e CustomEvent) EventName() string {
	if e.Name == "" {
		return "custom"
	}
	return "custom:" + e.Name
}

// Bus is the fan-out hub. The Agent owns one Bus; Subscribers come and go.
type Bus struct {
	mu   sync.RWMutex
	subs []*Subscription
}

// NewBus constructs an empty Bus.
func NewBus() *Bus { return &Bus{} }

// Subscribe registers a new subscriber with the given channel buffer
// (minimum 1, default 64 if buffer <= 0).
func (b *Bus) Subscribe(buffer int) *Subscription {
	if buffer <= 0 {
		buffer = 64
	}
	s := &Subscription{
		bus: b,
		ch:  make(chan Event, buffer),
	}
	b.mu.Lock()
	b.subs = append(b.subs, s)
	b.mu.Unlock()
	return s
}

// SubscriberCount returns the current number of subscribers. Intended for
// tests and diagnostics.
func (b *Bus) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

func (b *Bus) remove(s *Subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, x := range b.subs {
		if x == s {
			b.subs = append(b.subs[:i], b.subs[i+1:]...)
			return
		}
	}
}

// Emit publishes ev to all current subscribers. The send is non-blocking:
// if a subscriber's channel is full, the oldest pending event on that
// channel is dropped to make room.
func (b *Bus) Emit(ev Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, s := range b.subs {
		select {
		case s.ch <- ev:
		default:
			// drop oldest
			select {
			case <-s.ch:
			default:
			}
			// retry once; if still full (subscriber stopped draining) drop new
			select {
			case s.ch <- ev:
			default:
			}
		}
	}
}

// Subscription is the handle returned to a caller of Bus.Subscribe.
type Subscription struct {
	bus  *Bus
	ch   chan Event
	once sync.Once
}

// C exposes the read-only event channel.
func (s *Subscription) C() <-chan Event { return s.ch }

// Unsubscribe removes the subscription and closes the channel. Idempotent.
func (s *Subscription) Unsubscribe() {
	s.once.Do(func() {
		s.bus.remove(s)
		close(s.ch)
	})
}
