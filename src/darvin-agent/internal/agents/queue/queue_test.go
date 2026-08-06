package queue

import (
	"context"
	"testing"
	"time"
)

func TestEnqueueAndDequeue(t *testing.T) {
	q := New()
	if err := q.Enqueue(ModePrompt, Message{Content: "hello"}); err != nil {
		t.Fatalf("Enqueue prompt: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	m, mode, ok := q.Dequeue(ctx)
	if !ok {
		t.Fatal("Dequeue returned ok=false")
	}
	if mode != ModePrompt {
		t.Errorf("mode = %q, want prompt", mode)
	}
	if m.Content != "hello" {
		t.Errorf("content = %q, want hello", m.Content)
	}
}

func TestPrioritySteerOverPrompt(t *testing.T) {
	q := New()
	if err := q.Enqueue(ModePrompt, Message{Content: "p"}); err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(ModeSteer, Message{Content: "s"}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	m, mode, _ := q.Dequeue(ctx)
	if mode != ModeSteer || m.Content != "s" {
		t.Errorf("first dequeue = (%q, %q), want (s, steer)", m.Content, mode)
	}
	m, mode, _ = q.Dequeue(ctx)
	if mode != ModePrompt || m.Content != "p" {
		t.Errorf("second dequeue = (%q, %q), want (p, prompt)", m.Content, mode)
	}
}

func TestEnqueueFull(t *testing.T) {
	q := New()
	// prompt buffer = 1
	if err := q.Enqueue(ModePrompt, Message{Content: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(ModePrompt, Message{Content: "b"}); err != ErrQueueFull {
		t.Fatalf("second Enqueue err = %v, want ErrQueueFull", err)
	}
	// steer buffer = 1, distinct from prompt
	if err := q.Enqueue(ModeSteer, Message{Content: "s"}); err != nil {
		t.Fatalf("steer after prompt-full should succeed, got %v", err)
	}
}

func TestDequeueCancel(t *testing.T) {
	q := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, ok := q.Dequeue(ctx); ok {
		t.Error("Dequeue on cancelled ctx should return ok=false")
	}
}

func TestDequeueBlocksUntilMessage(t *testing.T) {
	q := New()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, ok := q.Dequeue(ctx)
	elapsed := time.Since(start)
	if ok {
		t.Error("Dequeue should not have returned a message")
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("Dequeue returned too early (%v), expected ~50ms", elapsed)
	}
}
