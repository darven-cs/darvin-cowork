package agent

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"darvin-cowork/backend/internal/agents/event"
	"darvin-cowork/backend/internal/llm"
	"darvin-cowork/backend/internal/agents/session"
	"darvin-cowork/backend/internal/agents/store"
)

// noopProvider implements llm.ModelProvider; the hook tests never drive a
// real run, so Complete/Stream just satisfy the interface.
type noopProvider struct{}

func (p *noopProvider) Name() string { return "noop" }
func (p *noopProvider) Complete(_ context.Context, _ *llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return nil, errors.New("not implemented")
}
func (p *noopProvider) Stream(_ context.Context, _ *llm.CompletionRequest) (*llm.StreamingResponse, error) {
	ch := make(chan llm.StreamEvent)
	close(ch)
	return llm.NewStreamingResponse(ch, nil), nil
}

// newTextDeltaHookStore returns a fresh SQLiteMessageStore on its own
// temp file with all GORM models migrated.
func newTextDeltaHookStore(t *testing.T) *store.SQLiteMessageStore {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "hook.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := db.AutoMigrate(&store.Session{}, &store.Message{}, &store.CompactionCheckpoint{}, &store.SkillSnapshot{}, &store.AppState{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	return store.NewSQLiteMessageStore(db)
}

// pollList repeatedly lists sessionID until got matches want, failing the
// test on timeout. The hook drains asynchronously, so assertions must poll.
func pollList(t *testing.T, ms store.MessageStore, sessionID, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		recs, err := ms.List(context.Background(), sessionID, 0, 0)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(recs) == 1 && recs[0].Content == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	recs, _ := ms.List(context.Background(), sessionID, 0, 0)
	t.Fatalf("content never reached %q: got %+v", want, recs)
}

// TestTextDeltaHook_AppendsToMatchingSession covers spec PR-1 hook
// behaviour: deltas for the hook's own session append to the matching
// message row; deltas for other sessions are filtered out.
func TestTextDeltaHook_AppendsToMatchingSession(t *testing.T) {
	ctx := context.Background()
	ms := newTextDeltaHookStore(t)
	if err := ms.Save(ctx, &store.MessageRecord{
		ID: "M", SessionID: "session-a", Role: "assistant", Content: "Hel", Timestamp: 1,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	a, err := New(NewAgentConfig{
		Session:      session.NewSession("session-a"),
		Provider:     &noopProvider{},
		MessageStore: ms,
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	h := NewTextDeltaHook(ms, zap.NewNop())
	h.Attach(a)
	defer h.Close()

	// Matching session + message: should accumulate "lo" and " world".
	a.Emit(event.TextDeltaEvent{
		EventBase: event.EventBase{EventCommon: event.EventCommon{
			SessionID: "session-a", MessageID: "M",
		}},
		Delta: "lo",
	})
	// Bogus session: filtered out before AppendContent.
	a.Emit(event.TextDeltaEvent{
		EventBase: event.EventBase{EventCommon: event.EventCommon{
			SessionID: "session-b", MessageID: "M",
		}},
		Delta: " XXX",
	})
	a.Emit(event.TextDeltaEvent{
		EventBase: event.EventBase{EventCommon: event.EventCommon{
			SessionID: "session-a", MessageID: "M",
		}},
		Delta: " world",
	})

	pollList(t, ms, "session-a", "Hello world")

	// The foreign-session delta must not have leaked a row.
	if n, err := ms.Count(ctx, "session-b"); err != nil {
		t.Fatalf("Count session-b: %v", err)
	} else if n != 0 {
		t.Errorf("Count session-b = %d, want 0 (foreign delta leaked)", n)
	}
}

// TestTextDeltaHook_IgnoresEmptyMessageID covers the guard that skips
// deltas with no message id — must not panic and must not write a row.
func TestTextDeltaHook_IgnoresEmptyMessageID(t *testing.T) {
	ctx := context.Background()
	ms := newTextDeltaHookStore(t)

	a, err := New(NewAgentConfig{
		Session:      session.NewSession("noop"),
		Provider:     &noopProvider{},
		MessageStore: ms,
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	h := NewTextDeltaHook(ms, zap.NewNop())
	h.Attach(a)
	defer h.Close()

	a.Emit(event.TextDeltaEvent{
		EventBase: event.EventBase{EventCommon: event.EventCommon{
			SessionID: "noop", MessageID: "",
		}},
		Delta: "dropped",
	})

	// Give the drain goroutine a chance to (not) process the event.
	time.Sleep(50 * time.Millisecond)
	if n, err := ms.Count(ctx, "noop"); err != nil {
		t.Fatalf("Count: %v", err)
	} else if n != 0 {
		t.Errorf("Count = %d, want 0 (empty messageID must be skipped)", n)
	}
}
