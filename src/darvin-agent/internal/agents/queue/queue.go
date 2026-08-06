// Package queue is the inbound message queue for an Agent. It exposes
// two channels (prompt, steer) with a strict priority order
// (steer > prompt) and a non-blocking Dequeue that respects ctx
// cancellation.
package queue

import (
	"context"
	"errors"

	"darvin-cowork/backend/internal/agents/event"
)

// Mode is a type alias for event.Mode so callers can use queue.Mode
// without importing the event package directly.
type Mode = event.Mode

const (
	ModePrompt = event.ModePrompt
	ModeSteer  = event.ModeSteer
)

// ErrQueueFull is returned by Enqueue when the corresponding channel
// buffer is full. Both prompt and steer use buffer 1 (rejected if
// busy); the harness closure immediately enqueues + dequeues one
// message per turn, so a full channel means a previous prompt has
// not yet been consumed.
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

// Queue owns the inbound channels. It is goroutine-safe.
type Queue struct {
	promptCh chan Message
	steerCh  chan Message
}

// New constructs a Queue with buffer 1 on each channel.
func New() *Queue {
	return &Queue{
		promptCh: make(chan Message, 1),
		steerCh:  make(chan Message, 1),
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
	default:
		return errors.New("queue: unknown mode")
	}
}

// Dequeue blocks until a message is available, ctx is cancelled, or
// both channels return empty and ctx fires. Priority is steer over
// prompt. On ctx cancel it returns (zero, "", false).
func (q *Queue) Dequeue(ctx context.Context) (Message, Mode, bool) {
	for {
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
		case <-ctx.Done():
			return Message{}, "", false
		case m := <-q.steerCh:
			return m, ModeSteer, true
		case m := <-q.promptCh:
			return m, ModePrompt, true
		}
	}
}

// Len returns the total number of buffered messages across both
// channels. Intended for diagnostics / tests.
func (q *Queue) Len() int {
	return len(q.promptCh) + len(q.steerCh)
}
