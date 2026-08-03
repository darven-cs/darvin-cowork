package event

import (
	"sync"
	"testing"
	"time"
)

func TestEmitFanout(t *testing.T) {
	bus := NewBus()
	s1 := bus.Subscribe(4)
	s2 := bus.Subscribe(4)
	defer s1.Unsubscribe()
	defer s2.Unsubscribe()

	bus.Emit(PromptReceivedEvent{Content: "hi", Mode: ModePrompt})
	bus.Emit(LLMStartEvent{Model: "m"})

	for i, s := range []*Subscription{s1, s2} {
		got1 := <-s.C()
		got2 := <-s.C()
		if got1.EventName() != "prompt_received" {
			t.Errorf("sub %d: first event = %q, want prompt_received", i, got1.EventName())
		}
		if got2.EventName() != "llm_start" {
			t.Errorf("sub %d: second event = %q, want llm_start", i, got2.EventName())
		}
	}
}

func TestDropOldestOnFullChannel(t *testing.T) {
	bus := NewBus()
	sub := bus.Subscribe(2) // capacity 2
	defer sub.Unsubscribe()

	// 5 events with no consumer → expect 2 newest kept
	for i := 0; i < 5; i++ {
		bus.Emit(TextDeltaEvent{Delta: string(rune('a' + i))})
	}

	got := make([]Event, 0, 2)
	timeout := time.After(100 * time.Millisecond)
	for len(got) < 2 {
		select {
		case ev := <-sub.C():
			got = append(got, ev)
		case <-timeout:
			t.Fatalf("timed out waiting for buffered events; got %d", len(got))
		}
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	// expect last two: 'd' and 'e'
	if got[0].(TextDeltaEvent).Delta != "d" {
		t.Errorf("got[0] = %q, want d", got[0].(TextDeltaEvent).Delta)
	}
	if got[1].(TextDeltaEvent).Delta != "e" {
		t.Errorf("got[1] = %q, want e", got[1].(TextDeltaEvent).Delta)
	}
}

func TestUnsubscribeIdempotent(t *testing.T) {
	bus := NewBus()
	sub := bus.Subscribe(1)
	if got := bus.SubscriberCount(); got != 1 {
		t.Fatalf("SubscriberCount = %d, want 1", got)
	}
	sub.Unsubscribe()
	sub.Unsubscribe() // must not panic
	if got := bus.SubscriberCount(); got != 0 {
		t.Fatalf("SubscriberCount after Unsubscribe = %d, want 0", got)
	}
	// reading from closed channel must not panic and must yield zero value
	select {
	case _, ok := <-sub.C():
		if ok {
			t.Error("channel should be closed")
		}
	case <-time.After(50 * time.Millisecond):
		t.Error("channel did not close after Unsubscribe")
	}
}

func TestCustomEventName(t *testing.T) {
	e := CustomEvent{Name: "skill_loaded", Payload: 42}
	if got := e.EventName(); got != "custom:skill_loaded" {
		t.Errorf("EventName = %q, want custom:skill_loaded", got)
	}
	e2 := CustomEvent{}
	if got := e2.EventName(); got != "custom" {
		t.Errorf("empty EventName = %q, want custom", got)
	}
}

func TestConcurrentSubscribeAndEmit(t *testing.T) {
	bus := NewBus()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := bus.Subscribe(8)
			bus.Emit(TextDeltaEvent{Delta: "x"})
			s.Unsubscribe()
		}()
	}
	wg.Wait()
}
