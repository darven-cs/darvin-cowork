// Tests for Manager. Uses an in-memory SubagentStore and a stub Runner
// so the orchestrator can be exercised without an LLM.

package subagent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"darvin-cowork/backend/internal/agents/protocol"
	"darvin-cowork/backend/internal/agents/store"
)

func newTestManager(t *testing.T, runner Runner) (*Manager, func()) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&store.Subagent{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	subStore := store.NewSQLiteSubagentStore(db)
	m := NewManager(Deps{
		Store:         subStore,
		ParentSession: "parent-1",
		Runner:        runner,
		MaxConcurrent: 4,
		ResultBufCap:  4096,
	})
	cleanup := func() { m.Close() }
	return m, cleanup
}

// stubRunner returns the canned result after sleeping for delay. Counts
// tool calls by parsing "<n> tool calls" out of the prompt; defaults to 0.
func stubRunner(delay time.Duration, result string, callCount *atomic.Int32) Runner {
	return func(ctx context.Context, req RunnerRequest) (RunnerResult, error) {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return RunnerResult{}, ctx.Err()
		}
		if callCount != nil {
			callCount.Add(1)
		}
		return RunnerResult{FinalText: result, ToolCalls: 0}, nil
	}
}

func TestManager_SpawnSyncSuccess(t *testing.T) {
	m, cleanup := newTestManager(t, stubRunner(10*time.Millisecond, "ok", nil))
	defer cleanup()

	info, err := m.Spawn(context.Background(), protocol.SubagentSpec{
		Prompt:      "do thing",
		Description: "thing",
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if info.Status != protocol.SubagentDone {
		t.Fatalf("want done, got %s", info.Status)
	}
	if info.ResultText != "ok" {
		t.Fatalf("want result 'ok', got %q", info.ResultText)
	}
	if info.ParentID != "parent-1" {
		t.Fatalf("want parent parent-1, got %s", info.ParentID)
	}
}

func TestManager_SpawnAsync(t *testing.T) {
	m, cleanup := newTestManager(t, stubRunner(50*time.Millisecond, "ok", nil))
	defer cleanup()

	info, err := m.Spawn(context.Background(), protocol.SubagentSpec{
		Prompt:          "do thing",
		RunInBackground: true,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if info.Status != protocol.SubagentRunning && info.Status != protocol.SubagentDone {
		t.Fatalf("want running or done on async return, got %s", info.Status)
	}
	got, err := m.Wait(info.ID, time.Second)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if got.Status != protocol.SubagentDone {
		t.Fatalf("want done after wait, got %s", got.Status)
	}
}

func TestManager_SpawnRejectsEmptyPrompt(t *testing.T) {
	m, cleanup := newTestManager(t, stubRunner(0, "", nil))
	defer cleanup()
	_, err := m.Spawn(context.Background(), protocol.SubagentSpec{Prompt: ""})
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("want ErrInvalidSpec, got %v", err)
	}
}

func TestManager_AbortCancelsRunning(t *testing.T) {
	release := make(chan struct{})
	blocking := func(ctx context.Context, req RunnerRequest) (RunnerResult, error) {
		select {
		case <-ctx.Done():
			return RunnerResult{}, ctx.Err()
		case <-release:
			return RunnerResult{FinalText: "ok"}, nil
		}
	}
	m, cleanup := newTestManager(t, blocking)
	defer cleanup()

	info, err := m.Spawn(context.Background(), protocol.SubagentSpec{
		Prompt:          "blocking",
		RunInBackground: true,
		TimeoutMs:       30000,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if err := m.Abort(info.ID); err != nil {
		t.Fatalf("abort: %v", err)
	}
	got, _ := m.Wait(info.ID, time.Second)
	if got.Status != protocol.SubagentAborted {
		t.Fatalf("want aborted after cancel, got %s", got.Status)
	}
	close(release)
}

func TestManager_ListSorted(t *testing.T) {
	m, cleanup := newTestManager(t, stubRunner(0, "ok", nil))
	defer cleanup()
	ids := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		info, err := m.Spawn(context.Background(), protocol.SubagentSpec{Prompt: fmt.Sprintf("p%d", i)})
		if err != nil {
			t.Fatalf("spawn %d: %v", i, err)
		}
		ids = append(ids, info.ID)
	}
	list := m.List()
	if len(list) != 3 {
		t.Fatalf("want 3 runs, got %d", len(list))
	}
	// List is sorted by StartedAt desc; the last spawn is first.
	if list[0].ID != ids[2] {
		t.Fatalf("want newest first (%s), got %s", ids[2], list[0].ID)
	}
}

func TestManager_GetUnknown(t *testing.T) {
	m, cleanup := newTestManager(t, stubRunner(0, "", nil))
	defer cleanup()
	_, err := m.Get("nonexistent")
	if !errors.Is(err, ErrUnknownID) {
		t.Fatalf("want ErrUnknownID, got %v", err)
	}
}

func TestManager_AbortUnknown(t *testing.T) {
	m, cleanup := newTestManager(t, stubRunner(0, "", nil))
	defer cleanup()
	if err := m.Abort("nope"); !errors.Is(err, ErrUnknownID) {
		t.Fatalf("want ErrUnknownID, got %v", err)
	}
}

func TestManager_ReadResult(t *testing.T) {
	m, cleanup := newTestManager(t, stubRunner(0, "abcdefghij", nil))
	defer cleanup()
	info, err := m.Spawn(context.Background(), protocol.SubagentSpec{Prompt: "p"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	got, err := m.ReadResult(info.ID, 3, 4)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != "defg" {
		t.Fatalf("want 'defg', got %q", got)
	}
}

func TestManager_CloseIdempotent(t *testing.T) {
	m, cleanup := newTestManager(t, stubRunner(0, "", nil))
	defer cleanup()
	m.Close()
	m.Close() // must not panic
}

func TestManager_CloseAbortsRunning(t *testing.T) {
	blocking := func(ctx context.Context, req RunnerRequest) (RunnerResult, error) {
		<-ctx.Done()
		return RunnerResult{}, ctx.Err()
	}
	m, _ := newTestManager(t, blocking)
	info, err := m.Spawn(context.Background(), protocol.SubagentSpec{
		Prompt:          "p",
		RunInBackground: true,
		TimeoutMs:       30000,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	m.Close()
	got, _ := m.Get(info.ID)
	if got.Status != protocol.SubagentAborted {
		t.Fatalf("want aborted after Close, got %s", got.Status)
	}
}

func TestManager_ConcurrencyLimit(t *testing.T) {
	var (
		inflight int32
		maxSeen  int32
		wg       sync.WaitGroup
	)
	slow := func(ctx context.Context, req RunnerRequest) (RunnerResult, error) {
		now := atomic.AddInt32(&inflight, 1)
		defer atomic.AddInt32(&inflight, -1)
		for {
			cur := atomic.LoadInt32(&maxSeen)
			if now <= cur || atomic.CompareAndSwapInt32(&maxSeen, cur, now) {
				break
			}
		}
		wg.Done()
		select {
		case <-time.After(30 * time.Millisecond):
		case <-ctx.Done():
			return RunnerResult{}, ctx.Err()
		}
		return RunnerResult{FinalText: "ok"}, nil
	}
	dsn := filepath.Join(t.TempDir(), "test.db")
	db, _ := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	_ = db.AutoMigrate(&store.Subagent{})
	m := NewManager(Deps{
		Store:         store.NewSQLiteSubagentStore(db),
		ParentSession: "p",
		Runner:        slow,
		MaxConcurrent: 3,
	})
	defer m.Close()
	for i := 0; i < 10; i++ {
		wg.Add(1)
		if _, err := m.Spawn(context.Background(), protocol.SubagentSpec{Prompt: "p"}); err != nil {
			t.Fatalf("spawn %d: %v", i, err)
		}
	}
	wg.Wait()
	if atomic.LoadInt32(&maxSeen) > 3 {
		t.Fatalf("MaxConcurrent=3 violated, saw %d concurrent", maxSeen)
	}
}

func TestManager_PersistsLifecycle(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "test.db")
	db, _ := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	_ = db.AutoMigrate(&store.Subagent{})
	subStore := store.NewSQLiteSubagentStore(db)
	m := NewManager(Deps{
		Store:         subStore,
		ParentSession: "p",
		Runner:        stubRunner(0, "result", nil),
	})
	defer m.Close()
	info, err := m.Spawn(context.Background(), protocol.SubagentSpec{Prompt: "p"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	row, err := subStore.Get(context.Background(), info.ID)
	if err != nil {
		t.Fatalf("store get: %v", err)
	}
	if row.Status != string(protocol.SubagentDone) {
		t.Fatalf("want stored status done, got %s", row.Status)
	}
	if row.ResultText != "result" {
		t.Fatalf("want stored result 'result', got %q", row.ResultText)
	}
}

func TestContextRoundTrip(t *testing.T) {
	m, cleanup := newTestManager(t, stubRunner(0, "", nil))
	defer cleanup()
	ctx := WithContext(context.Background(), m)
	got, ok := FromContext(ctx)
	if !ok || got != m {
		t.Fatalf("context roundtrip lost pointer")
	}
	_, ok = FromContext(context.Background())
	if ok {
		t.Fatalf("want no manager in plain ctx")
	}
}
