package msgid

import (
	"sync"
	"testing"
)

func TestAttachAndRead(t *testing.T) {
	b := NewBridge()
	b.AttachMessageID(func() string { return "msg-1" })
	b.AttachRunID(func() string { return "run-1" })
	b.AttachUserMessageID(func() string { return "user-1" })

	if got := b.CurrentMessageID(); got != "msg-1" {
		t.Fatalf("CurrentMessageID = %q, want msg-1", got)
	}
	if got := b.CurrentRunID(); got != "run-1" {
		t.Fatalf("CurrentRunID = %q, want run-1", got)
	}
	if got := b.CurrentUserMessageID(); got != "user-1" {
		t.Fatalf("CurrentUserMessageID = %q, want user-1", got)
	}
}

func TestNilSrcReturnsEmpty(t *testing.T) {
	b := NewBridge()
	if got := b.CurrentMessageID(); got != "" {
		t.Fatalf("CurrentMessageID = %q, want empty", got)
	}
	if got := b.CurrentRunID(); got != "" {
		t.Fatalf("CurrentRunID = %q, want empty", got)
	}
	if got := b.CurrentUserMessageID(); got != "" {
		t.Fatalf("CurrentUserMessageID = %q, want empty", got)
	}
}

func TestConcurrentAttachAndRead(t *testing.T) {
	b := NewBridge()
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			b.AttachMessageID(func() string { return "m" })
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			b.AttachRunID(func() string { return "r" })
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			_ = b.CurrentMessageID()
			_ = b.CurrentRunID()
		}
	}()
	wg.Wait()
}

func TestAttachReplacesSource(t *testing.T) {
	b := NewBridge()
	b.AttachMessageID(func() string { return "first" })
	b.AttachMessageID(func() string { return "second" })
	if got := b.CurrentMessageID(); got != "second" {
		t.Fatalf("CurrentMessageID = %q, want second", got)
	}
}
