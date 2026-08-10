// Tests for the 5 subagent_* built-in tools. Uses a stub
// SubagentStore + stub runner to exercise the tool surface; the
// real DB writes happen in production wiring (see manager_test.go).

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"darvin-cowork/backend/internal/agents/protocol"
	"darvin-cowork/backend/internal/agents/store"
	"darvin-cowork/backend/internal/subagent"
)

func toolTestRunner(delay time.Duration, text string, calls *atomic.Int32) subagent.Runner {
	return func(ctx context.Context, req subagent.RunnerRequest) (subagent.RunnerResult, error) {
		if calls != nil {
			calls.Add(1)
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return subagent.RunnerResult{}, ctx.Err()
		}
		return subagent.RunnerResult{FinalText: text, ToolCalls: 0}, nil
	}
}

func newTestMgrForTools(t *testing.T, runner subagent.Runner) *subagent.Manager {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "tools-test.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// SetMaxOpenConns(1) before AutoMigrate to lock all handles (incl.
	// the migrator's pool) to one connection — otherwise concurrent
	// writers race and glebarez/sqlite returns SQLITE_READONLY_DBMOVED
	// on the second handle against the tmpfs temp dir.
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&store.Subagent{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	mgr := subagent.NewManager(subagent.Deps{
		Store:         store.NewSQLiteSubagentStore(db),
		ParentSession: "test-session",
		Runner:        runner,
		MaxConcurrent: 4,
	})
	t.Cleanup(mgr.Close)
	return mgr
}

func TestSubagentTools_Registered(t *testing.T) {
	for _, name := range []string{
		"delegate_subagent",
		"list_subagents",
		"abort_subagent",
		"parallel_subagents",
		"read_subagent_result",
	} {
		ok := false
		for _, n := range RegisteredBuiltinFactories() {
			if n == name {
				ok = true
				break
			}
		}
		if !ok {
			t.Fatalf("tool %q not registered", name)
		}
	}
}

func TestDelegateSubagent_MissingCtx(t *testing.T) {
	res := delegateSubagentTool{}.Execute(context.Background(), map[string]any{"prompt": "x", "description": "d"})
	if !res.IsError {
		t.Fatalf("want error when no manager in ctx")
	}
}

func TestDelegateSubagent_SyncSuccess(t *testing.T) {
	mgr := newTestMgrForTools(t, toolTestRunner(0, "hello", nil))
	ctx := subagent.WithContext(context.Background(), mgr)
	res := delegateSubagentTool{}.Execute(ctx, map[string]any{
		"prompt":      "say hello",
		"description": "greet",
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "status: done") || !strings.Contains(res.Content, "hello") {
		t.Fatalf("want done + hello, got %q", res.Content)
	}
}

func TestDelegateSubagent_AsyncReturns(t *testing.T) {
	mgr := newTestMgrForTools(t, toolTestRunner(300*time.Millisecond, "x", nil))
	ctx := subagent.WithContext(context.Background(), mgr)
	res := delegateSubagentTool{}.Execute(ctx, map[string]any{
		"prompt":            "p",
		"description":       "d",
		"run_in_background": true,
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.HasPrefix(res.Content, "started background subagent ") {
		t.Fatalf("want async return, got %q", res.Content)
	}
}

func TestDelegateSubagent_RequiresPrompt(t *testing.T) {
	mgr := newTestMgrForTools(t, toolTestRunner(0, "", nil))
	ctx := subagent.WithContext(context.Background(), mgr)
	res := delegateSubagentTool{}.Execute(ctx, map[string]any{"description": "d"})
	if !res.IsError {
		t.Fatalf("want error for missing prompt")
	}
}

func TestListSubagents(t *testing.T) {
	mgr := newTestMgrForTools(t, toolTestRunner(0, "x", nil))
	ctx := subagent.WithContext(context.Background(), mgr)
	for i := 0; i < 3; i++ {
		if _, err := mgr.Spawn(ctx, protocol.SubagentSpec{
			Prompt: fmt.Sprintf("task-%d", i), Description: "d",
		}); err != nil {
			t.Fatalf("spawn %d: %v", i, err)
		}
	}
	res := listSubagentsTool{}.Execute(ctx, nil)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	lines := strings.Split(strings.TrimSpace(res.Content), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d: %q", len(lines), res.Content)
	}
}

func TestListSubagents_Empty(t *testing.T) {
	mgr := newTestMgrForTools(t, toolTestRunner(0, "x", nil))
	ctx := subagent.WithContext(context.Background(), mgr)
	res := listSubagentsTool{}.Execute(ctx, nil)
	if res.Content != "no subagents" {
		t.Fatalf("want empty message, got %q", res.Content)
	}
}

func TestAbortSubagent(t *testing.T) {
	release := make(chan struct{})
	blocking := func(ctx context.Context, req subagent.RunnerRequest) (subagent.RunnerResult, error) {
		select {
		case <-ctx.Done():
			return subagent.RunnerResult{}, ctx.Err()
		case <-release:
			return subagent.RunnerResult{FinalText: "x"}, nil
		}
	}
	mgr := newTestMgrForTools(t, blocking)
	ctx := subagent.WithContext(context.Background(), mgr)
	info, err := mgr.Spawn(ctx, protocol.SubagentSpec{
		Prompt: "p", Description: "d", RunInBackground: true, TimeoutMs: 30000,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	res := abortSubagentTool{}.Execute(ctx, map[string]any{"id": info.ID})
	if res.IsError {
		t.Fatalf("abort failed: %s", res.Content)
	}
	if !strings.HasPrefix(res.Content, "aborted subagent ") {
		t.Fatalf("want 'aborted subagent' prefix, got %q", res.Content)
	}
	close(release)
	res = abortSubagentTool{}.Execute(ctx, map[string]any{"id": "missing"})
	if !res.IsError {
		t.Fatalf("want error for missing id")
	}
}

func TestParallelSubagents_BlocksAndCombines(t *testing.T) {
	var calls atomic.Int32
	mgr := newTestMgrForTools(t, toolTestRunner(0, "ok", &calls))
	ctx := subagent.WithContext(context.Background(), mgr)
	res := parallelSubagentsTool{}.Execute(ctx, map[string]any{
		"tasks": []any{
			map[string]any{"prompt": "a", "description": "A"},
			map[string]any{"prompt": "b", "description": "B"},
			map[string]any{"prompt": "c", "description": "C"},
		},
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	sections := strings.Split(res.Content, "---")
	if len(sections) != 3 {
		t.Fatalf("want 3 sections, got %d:\n%s", len(sections), res.Content)
	}
	if calls.Load() != 3 {
		t.Fatalf("want 3 runner calls, got %d", calls.Load())
	}
}

func TestParallelSubagents_OverLimit(t *testing.T) {
	mgr := newTestMgrForTools(t, toolTestRunner(0, "x", nil))
	ctx := subagent.WithContext(context.Background(), mgr)
	tasks := make([]any, 0, maxParallelTasks+1)
	for i := 0; i <= maxParallelTasks; i++ {
		tasks = append(tasks, map[string]any{"prompt": "p", "description": "d"})
	}
	res := parallelSubagentsTool{}.Execute(ctx, map[string]any{"tasks": tasks})
	if !res.IsError {
		t.Fatalf("want error when tasks > %d", maxParallelTasks)
	}
}

func TestParallelSubagents_EmptyTasks(t *testing.T) {
	mgr := newTestMgrForTools(t, toolTestRunner(0, "x", nil))
	ctx := subagent.WithContext(context.Background(), mgr)
	res := parallelSubagentsTool{}.Execute(ctx, map[string]any{"tasks": []any{}})
	if !res.IsError {
		t.Fatalf("want error for empty tasks")
	}
}

func TestReadSubagentResult(t *testing.T) {
	mgr := newTestMgrForTools(t, toolTestRunner(0, strings.Repeat("x", 100), nil))
	ctx := subagent.WithContext(context.Background(), mgr)
	info, err := mgr.Spawn(ctx, protocol.SubagentSpec{Prompt: "p", Description: "d"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	res := readSubagentResultTool{}.Execute(ctx, map[string]any{
		"ref":          info.ID,
		"offset_bytes": 10,
		"limit_bytes":  20,
	})
	if res.IsError {
		t.Fatalf("read failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "[offset 10, returned") {
		t.Fatalf("want header in output, got %q", res.Content)
	}
}

func TestReadSubagentResult_UnknownID(t *testing.T) {
	mgr := newTestMgrForTools(t, toolTestRunner(0, "", nil))
	ctx := subagent.WithContext(context.Background(), mgr)
	res := readSubagentResultTool{}.Execute(ctx, map[string]any{"ref": "missing"})
	if !res.IsError {
		t.Fatalf("want error for missing id")
	}
}

func TestReadSubagentResult_ClampLimit(t *testing.T) {
	mgr := newTestMgrForTools(t, toolTestRunner(0, "x", nil))
	ctx := subagent.WithContext(context.Background(), mgr)
	info, err := mgr.Spawn(ctx, protocol.SubagentSpec{Prompt: "p", Description: "d"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	res := readSubagentResultTool{}.Execute(ctx, map[string]any{
		"ref":         info.ID,
		"limit_bytes": 100 * 1024,
	})
	if res.IsError {
		t.Fatalf("read failed: %s", res.Content)
	}
}

func TestSubagentTools_SchemaValid(t *testing.T) {
	for name, t0 := range map[string]Tool{
		"delegate_subagent":    delegateSubagentTool{},
		"list_subagents":       listSubagentsTool{},
		"abort_subagent":       abortSubagentTool{},
		"parallel_subagents":   parallelSubagentsTool{},
		"read_subagent_result": readSubagentResultTool{},
	} {
		var schema map[string]any
		if err := json.Unmarshal(t0.Parameters(), &schema); err != nil {
			t.Fatalf("%s schema invalid JSON: %v", name, err)
		}
		if schema["type"] != "object" {
			t.Fatalf("%s schema type = %v, want object", name, schema["type"])
		}
	}
}

// keep imports referenced even when only some are used by every test.
var _ = sync.Once{}
