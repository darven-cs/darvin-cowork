package usage

import (
	"sync"
	"testing"

	"darvin-cowork/backend/internal/agents/protocol"
)

func TestRecordAndRead(t *testing.T) {
	tr := NewTracker()
	if got := tr.Last(); got != (protocol.Usage{}) {
		t.Fatalf("initial Last = %+v, want zero", got)
	}
	tr.Record(protocol.Usage{PromptTokens: 12, CompletionTokens: 34, TotalTokens: 46})
	if got := tr.Last(); got.PromptTokens != 12 || got.CompletionTokens != 34 || got.TotalTokens != 46 {
		t.Fatalf("Last = %+v, want 12/34/46", got)
	}
}

func TestRecordReplaces(t *testing.T) {
	tr := NewTracker()
	tr.Record(protocol.Usage{PromptTokens: 1})
	tr.Record(protocol.Usage{PromptTokens: 2})
	if got := tr.Last(); got.PromptTokens != 2 {
		t.Fatalf("Last.PromptTokens = %d, want 2 (the latest record)", got.PromptTokens)
	}
}

func TestConcurrentReadsAndWrites(t *testing.T) {
	tr := NewTracker()
	tr.Record(protocol.Usage{TotalTokens: 1})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			tr.Record(protocol.Usage{TotalTokens: i})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			_ = tr.Last()
		}
	}()
	wg.Wait()
}
