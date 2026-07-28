// Package event defines the agent lifecycle event protocol and a fan-out bus
// used by the Agent to publish events to multiple subscribers.
//
// Events are sealed-sum types (interface + unexported marker). Subscribers
// register via Bus.Subscribe; events are dropped (oldest first) on a full
// channel to keep the agent's main loop non-blocking.
package event

import (
	"sync"

	"darvin-cowork/backend/internal/agent/llm"
)

// Mode mirrors the queue's PromptMode so PromptReceivedEvent can carry the
// origin of the message without depending on the queue package.
type Mode string

const (
	ModePrompt   Mode = "prompt"
	ModeSteer    Mode = "steer"
	ModeFollowUp Mode = "followup"
)

// Event is the sealed agent lifecycle event. All concrete event types
// implement the unexported isAgentEvent marker.
type Event interface {
	isAgentEvent()
	EventName() string
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
	Content string
	Mode    Mode
}

func (PromptReceivedEvent) isAgentEvent()     {}
func (PromptReceivedEvent) EventName() string { return "prompt_received" }

// RunStartEvent marks the start of a single Run (one or more turns serving
// the next dequeued message).
type RunStartEvent struct {
	SessionID string
}

func (RunStartEvent) isAgentEvent()     {}
func (RunStartEvent) EventName() string { return "run_start" }

// TurnStartEvent marks the beginning of one LLM round-trip.
type TurnStartEvent struct {
	TurnID    string
	TurnIndex int
}

func (TurnStartEvent) isAgentEvent()     {}
func (TurnStartEvent) EventName() string { return "turn_start" }

// LLMStartEvent is emitted right before provider.Stream is called.
type LLMStartEvent struct {
	Model string
}

func (LLMStartEvent) isAgentEvent()     {}
func (LLMStartEvent) EventName() string { return "llm_start" }

// TextDeltaEvent is a passthrough of provider.TextDeltaEvent.
type TextDeltaEvent struct {
	Delta string
}

func (TextDeltaEvent) isAgentEvent()     {}
func (TextDeltaEvent) EventName() string { return "text_delta" }

// LLMEndEvent fires after the provider's stream closes (DoneEvent or
// ErrorEvent), with the accumulated assistant message and usage.
type LLMEndEvent struct {
	Assistant llm.Message
	Usage     llm.Usage
}

func (LLMEndEvent) isAgentEvent()     {}
func (LLMEndEvent) EventName() string { return "llm_end" }

// ToolStartEvent is emitted before a tool is invoked. One per call when the
// assistant issued multiple tool calls in a single turn.
type ToolStartEvent struct {
	TurnID    string
	CallID    string
	Name      string
	Arguments map[string]any
}

func (ToolStartEvent) isAgentEvent()     {}
func (ToolStartEvent) EventName() string { return "tool_start" }

// ToolEndEvent fires after a tool's Execute returns. Duration is measured
// inside the executor goroutine.
type ToolEndEvent struct {
	CallID     string
	Result     ToolResult
	DurationMS int64
}

func (ToolEndEvent) isAgentEvent()     {}
func (ToolEndEvent) EventName() string { return "tool_end" }

// TurnEndEvent closes a single turn.
type TurnEndEvent struct {
	TurnIndex  int
	StopReason llm.FinishReason
}

func (TurnEndEvent) isAgentEvent()     {}
func (TurnEndEvent) EventName() string { return "turn_end" }

// RunEndEvent marks the end of a Run (all turns for one dequeued message).
type RunEndEvent struct {
	Turns int
}

func (RunEndEvent) isAgentEvent()     {}
func (RunEndEvent) EventName() string { return "run_end" }

// AgentErrorEvent signals a non-fatal error. The Agent may still produce
// further events; a terminal abort is signalled by FinishReasonAborted
// in the corresponding TurnEndEvent / AgentEndEvent.
type AgentErrorEvent struct {
	Err error
}

func (AgentErrorEvent) isAgentEvent()     {}
func (AgentErrorEvent) EventName() string { return "agent_error" }

// AgentEndEvent marks the very end of Agent.Run. After this, the Agent
// returns to idle and may accept new prompts (or auto-drain FollowUp).
type AgentEndEvent struct {
	SessionID  string
	TotalTurns int
	TotalUsage llm.Usage
}

func (AgentEndEvent) isAgentEvent()     {}
func (AgentEndEvent) EventName() string { return "agent_end" }

// CompactionEvent is reserved for the future ContextEngine spec. The agent
// loop never emits it in the current milestone.
type CompactionEvent struct {
	Before int
	After  int
	Note   string
}

func (CompactionEvent) isAgentEvent()     {}
func (CompactionEvent) EventName() string { return "compaction" }

// CustomEvent is an out-of-band channel for future specs (e.g. Skills / MCP)
// to publish domain-specific events without expanding the agent core.
type CustomEvent struct {
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
