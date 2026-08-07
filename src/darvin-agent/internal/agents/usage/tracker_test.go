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

func TestRecordWithModelAccumulatesTotal(t *testing.T) {
	tr := NewTracker()
	tr.RecordWithModel(protocol.Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120, CacheReadTokens: 5}, "claude-opus")
	tr.RecordWithModel(protocol.Usage{PromptTokens: 200, CompletionTokens: 30, TotalTokens: 230, CacheReadTokens: 15}, "claude-opus")

	snap := tr.Snapshot()
	if snap.Last == nil {
		t.Fatal("Snapshot.Last = nil, want populated")
	}
	if snap.Last.PromptTokens != 200 || snap.Last.CompletionTokens != 30 {
		t.Fatalf("Snapshot.Last = %+v, want prompt=200 completion=30", snap.Last)
	}
	if snap.LastModel != "claude-opus" {
		t.Fatalf("Snapshot.LastModel = %q, want claude-opus", snap.LastModel)
	}
	if snap.RequestCount != 2 {
		t.Fatalf("Snapshot.RequestCount = %d, want 2", snap.RequestCount)
	}
	if snap.Total == nil {
		t.Fatal("Snapshot.Total = nil, want populated")
	}
	if snap.Total.PromptTokens != 300 || snap.Total.CompletionTokens != 50 || snap.Total.CacheReadTokens != 20 {
		t.Fatalf("Snapshot.Total = %+v, want prompt=300 completion=50 cacheRead=20", snap.Total)
	}
	if snap.UpdatedAt <= 0 {
		t.Fatalf("Snapshot.UpdatedAt = %d, want positive unix ms", snap.UpdatedAt)
	}
}

func TestRecordWithoutModelKeepsBackwardCompat(t *testing.T) {
	tr := NewTracker()
	tr.Record(protocol.Usage{PromptTokens: 50})
	snap := tr.Snapshot()
	if snap.LastModel != "" {
		t.Fatalf("Snapshot.LastModel = %q, want empty when model not supplied", snap.LastModel)
	}
	if snap.Last == nil || snap.Last.PromptTokens != 50 {
		t.Fatalf("Snapshot.Last = %+v, want prompt=50", snap.Last)
	}
}

func TestSnapshotEmptyBeforeAnyRecord(t *testing.T) {
	tr := NewTracker()
	snap := tr.Snapshot()
	if snap.Last != nil {
		t.Fatalf("Snapshot.Last = %+v, want nil on empty tracker", snap.Last)
	}
	if snap.Total != nil {
		t.Fatalf("Snapshot.Total = %+v, want nil on empty tracker", snap.Total)
	}
	if snap.LastModel != "" {
		t.Fatalf("Snapshot.LastModel = %q, want empty", snap.LastModel)
	}
	if snap.UpdatedAt <= 0 {
		t.Fatalf("Snapshot.UpdatedAt = %d, want positive unix ms", snap.UpdatedAt)
	}
}

func TestResetClearsCumulative(t *testing.T) {
	tr := NewTracker()
	tr.RecordWithModel(protocol.Usage{PromptTokens: 100, CompletionTokens: 20}, "claude-opus")
	tr.RecordWithModel(protocol.Usage{PromptTokens: 50, CompletionTokens: 10}, "claude-opus")
	if got := tr.Last().PromptTokens; got != 50 {
		t.Fatalf("before Reset, Last.PromptTokens = %d, want 50", got)
	}

	tr.Reset()
	if got := tr.Last(); got != (protocol.Usage{}) {
		t.Fatalf("after Reset, Last = %+v, want zero", got)
	}
	if got := tr.LastModel(); got != "" {
		t.Fatalf("after Reset, LastModel = %q, want empty", got)
	}
	snap := tr.Snapshot()
	if snap.Last != nil || snap.Total != nil {
		t.Fatalf("after Reset, Snapshot = %+v, want Last=nil Total=nil", snap)
	}
	if snap.RequestCount != 0 {
		t.Fatalf("after Reset, Snapshot.RequestCount = %d, want 0", snap.RequestCount)
	}
}

func TestSnapshotIsValueCopyNotAliased(t *testing.T) {
	tr := NewTracker()
	tr.RecordWithModel(protocol.Usage{PromptTokens: 100}, "claude-opus")
	snap := tr.Snapshot()
	snap.Last.PromptTokens = 999
	snap.Total.PromptTokens = 999
	if got := tr.Last().PromptTokens; got != 100 {
		t.Fatalf("mutating Snapshot.Last leaked into Tracker.Last (got %d)", got)
	}
}