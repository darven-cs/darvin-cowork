package acp

import (
	"context"

	"darvin-cowork/backend/internal/agent/queue"
)

// Queue is a thin wrapper over agent/queue.Queue that keeps the agent
// package from leaking through the acp API. Use NewQueue to construct
// one — the default configuration matches agent/queue.New.
type Queue struct{ inner *queue.Queue }

// NewQueue constructs an empty Queue.
func NewQueue() *Queue { return &Queue{inner: queue.New()} }

// Enqueue adds content to the queue under the given mode (Prompt /
// Steer / FollowUp). See agent/queue for the full semantics.
func (q *Queue) Enqueue(mode queue.Mode, content string) error {
	return q.inner.Enqueue(mode, queue.Message{Content: content})
}

// Dequeue blocks until a message is available or ctx is cancelled.
// Returns the message, its mode, and a boolean indicating whether a
// message was actually dequeued.
func (q *Queue) Dequeue(ctx context.Context) (queue.Message, queue.Mode, bool) {
	return q.inner.Dequeue(ctx)
}

// Len reports the current queue depth.
func (q *Queue) Len() int { return q.inner.Len() }
