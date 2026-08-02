// Package queue is the inbound message queue for an Agent. It exposes three
// channels (prompt, steer, followup) with a strict priority order
// (steer > prompt > followup) and a non-blocking Dequeue that respects
// ctx cancellation.
package queue

import (
	"context"
	"errors"

	"darvin-cowork/backend/internal/agent/event"
)

// Mode is a type alias for event.Mode so callers can use queue.Mode without
// importing the event package directly.
type Mode = event.Mode

const (
	ModePrompt   = event.ModePrompt
	ModeSteer    = event.ModeSteer
	ModeFollowUp = event.ModeFollowUp
)

// ErrQueueFull is returned by Enqueue when the corresponding channel buffer
// is full. Prompt / Steer use buffer 1 (rejected if busy); FollowUp uses
// buffer 16 (rejected only if the backlog is enormous).
var ErrQueueFull = errors.New("queue: channel full")

// Message is the unit of work carried by the queue.
type Message struct {
	Content string
	// Attachments carries absolute paths the user attached for this one
	// message ("attach = authorize"): the dispatcher injects a system note so
	// the LLM perceives them, and grants read_file access to them.
	Attachments []string
	// Images carries base64-encoded images attached for this message; the
	// dispatcher converts each DataURL into an llm.ImageBlock so the provider
	// receives a real image content block.
	Images []ImageRef
}

// ImageRef is a base64-encoded image attachment staged for one message.
// DataURL has the `data:<mime>;base64,<data>` shape produced by the
// renderer's readFileAsDataUrl; the dispatcher splits it into the LLM's
// {MediaType, Data}.
type ImageRef struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	DataURL string `json:"dataUrl"`
}

// Queue owns the three inbound channels. It is goroutine-safe.
type Queue struct {
	promptCh   chan Message
	steerCh    chan Message
	followupCh chan Message
}

// New constructs a Queue with the standard buffer sizes (prompt 1, steer 1,
// followup 16). FollowUp buffer is sized to absorb a normal user burst.
func New() *Queue {
	return &Queue{
		promptCh:   make(chan Message, 1),
		steerCh:    make(chan Message, 1),
		followupCh: make(chan Message, 16),
	}
}

// Enqueue places msg into the channel for the given mode. Returns
// ErrQueueFull if the channel buffer is full.
func (q *Queue) Enqueue(mode Mode, msg Message) error {
	switch mode {
	case ModePrompt:
		select {
		case q.promptCh <- msg:
			return nil
		default:
			return ErrQueueFull
		}
	case ModeSteer:
		select {
		case q.steerCh <- msg:
			return nil
		default:
			return ErrQueueFull
		}
	case ModeFollowUp:
		select {
		case q.followupCh <- msg:
			return nil
		default:
			return ErrQueueFull
		}
	default:
		return errors.New("queue: unknown mode")
	}
}

// Dequeue blocks until a message is available, ctx is cancelled, or all
// three channels return empty and ctx fires. Priority is steer > prompt >
// followup. On ctx cancel it returns (zero, "", false).
func (q *Queue) Dequeue(ctx context.Context) (Message, Mode, bool) {
	for {
		// non-blocking check: prefer steer, then prompt, then followup
		select {
		case m := <-q.steerCh:
			return m, ModeSteer, true
		default:
		}
		select {
		case m := <-q.promptCh:
			return m, ModePrompt, true
		default:
		}
		select {
		case m := <-q.followupCh:
			return m, ModeFollowUp, true
		default:
		}
		// block until something arrives or ctx fires
		select {
		case <-ctx.Done():
			return Message{}, "", false
		case m := <-q.steerCh:
			return m, ModeSteer, true
		case m := <-q.promptCh:
			return m, ModePrompt, true
		case m := <-q.followupCh:
			return m, ModeFollowUp, true
		}
	}
}

// Len returns the total number of buffered messages across all three
// channels. Intended for diagnostics / tests.
func (q *Queue) Len() int {
	return len(q.promptCh) + len(q.steerCh) + len(q.followupCh)
}
