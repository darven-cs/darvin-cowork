// Package queue is the Agent's inbound message queue: two channels
// (prompt, steer) with strict priority (steer > prompt) and a
// non-blocking Dequeue that respects ctx cancellation.
package queue

import (
	"context"
	"errors"

	"darvin-cowork/backend/internal/agents/event"
)

// Mode is a type alias for event.Mode so callers use queue.Mode directly.
type Mode = event.Mode

const (
	ModePrompt = event.ModePrompt
	ModeSteer  = event.ModeSteer
)

// ErrQueueFull is returned by Enqueue when the channel buffer is full
// (both channels use buffer 1; the harness enqueues + dequeues one message
// per turn, so a full channel means a previous prompt is unconsumed).
var ErrQueueFull = errors.New("queue: channel full")

// Message is the unit of work carried by the queue.
type Message struct {
	Content string
	// Attachments are absolute paths staged for this message
	// ("attach = authorize"): the dispatcher injects a system note and
	// grants read_file access.
	Attachments []string
	// Images are base64 attachments; the dispatcher converts each
	// DataURL into an llm.ImageBlock.
	Images []ImageRef
}

// ImageRef is a base64-encoded image staged for one message, in the
// `data:<mime>;base64,<data>` shape the renderer produces.
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

// Enqueue places msg into the channel for the given mode, or returns
// ErrQueueFull when the buffer is full.
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

// Dequeue blocks until a message is available (steer > prompt) or ctx
// is cancelled, returning (zero, "", false) on cancel.
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

// Len returns the total buffered messages across both channels.
func (q *Queue) Len() int {
	return len(q.promptCh) + len(q.steerCh)
}
