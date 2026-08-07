package agentloop

import (
	"context"
	"sort"
	"sync"
	"testing"

	"darvin-cowork/backend/internal/agents/protocol"
	"darvin-cowork/backend/internal/agents/store"
)

// fakeMessageStore is an in-memory MessageStore used to test hydration
// without pulling in SQLite.
type fakeMessageStore struct {
	mu   sync.Mutex
	rows map[string][]store.MessageRecord
}

func newFakeMessageStore() *fakeMessageStore {
	return &fakeMessageStore{rows: map[string][]store.MessageRecord{}}
}

func (f *fakeMessageStore) Save(_ context.Context, m *store.MessageRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[m.SessionID] = append(f.rows[m.SessionID], *m)
	return nil
}

func (f *fakeMessageStore) List(_ context.Context, sessionID string, _limit, _offset int) ([]store.MessageRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]store.MessageRecord, len(f.rows[sessionID]))
	copy(out, f.rows[sessionID])
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp < out[j].Timestamp })
	return out, nil
}

func (f *fakeMessageStore) Count(_ context.Context, _ string) (int, error) { return 0, nil }
func (f *fakeMessageStore) AppendContent(_ context.Context, _ string, _ string) error {
	return nil
}
func (f *fakeMessageStore) MarkDone(_ context.Context, _ string) error { return nil }
func (f *fakeMessageStore) MarkError(_ context.Context, _ string, _ string) error {
	return nil
}
func (f *fakeMessageStore) DeleteBySession(_ context.Context, _ string) error { return nil }

func factoryWithMessageStore(ms store.MessageStore) *AgentFactory {
	f := newTestFactory()
	f.MessageStore = ms
	return f
}

func TestHydrate_RestoresConversationInOrder(t *testing.T) {
	ms := newFakeMessageStore()
	now := int64(1000)
	for _, r := range []store.MessageRecord{
		{ID: "u1", SessionID: "alpha", Role: "user", Content: "hi", Timestamp: now, Done: true},
		{ID: "a1", SessionID: "alpha", Role: "assistant", Content: "hello", Timestamp: now + 10, Done: true},
		{ID: "u2", SessionID: "alpha", Role: "user", Content: "who are you", Timestamp: now + 20, Done: true},
		{ID: "a2", SessionID: "alpha", Role: "assistant", Content: "I'm a bot", Timestamp: now + 30, Done: true},
	} {
		if err := ms.Save(context.Background(), &r); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	f := factoryWithMessageStore(ms)
	sess, err := f.NewAgentLoopSession("alpha")
	if err != nil {
		t.Fatalf("NewAgentLoopSession: %v", err)
	}
	t.Cleanup(sess.Close)

	msgs := sess.Agent.Session().Messages()
	if len(msgs) != 4 {
		t.Fatalf("hydrated messages = %d, want 4", len(msgs))
	}
	want := []struct {
		role    protocol.Role
		content string
	}{
		{protocol.RoleUser, "hi"},
		{protocol.RoleAssistant, "hello"},
		{protocol.RoleUser, "who are you"},
		{protocol.RoleAssistant, "I'm a bot"},
	}
	for i, w := range want {
		if msgs[i].Role != w.role || msgs[i].Content != w.content {
			t.Errorf("msg[%d] = (%s, %q), want (%s, %q)", i, msgs[i].Role, msgs[i].Content, w.role, w.content)
		}
	}
}

func TestHydrate_RebuildsToolResultMessages(t *testing.T) {
	ms := newFakeMessageStore()
	toolCalls := `[{"id":"call_1","name":"bash","arguments":{"cmd":"ls"},"result":{"content":"file.txt","isError":false}}]`
	for _, r := range []store.MessageRecord{
		{ID: "u1", SessionID: "alpha", Role: "user", Content: "run ls", Timestamp: 1, Done: true},
		{ID: "a1", SessionID: "alpha", Role: "assistant", Content: "", ToolCalls: toolCalls, Timestamp: 2, Done: true},
	} {
		if err := ms.Save(context.Background(), &r); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	f := factoryWithMessageStore(ms)
	sess, err := f.NewAgentLoopSession("alpha")
	if err != nil {
		t.Fatalf("NewAgentLoopSession: %v", err)
	}
	t.Cleanup(sess.Close)

	msgs := sess.Agent.Session().Messages()
	if len(msgs) != 3 {
		t.Fatalf("hydrated messages = %d, want 3 (user + assistant + tool)", len(msgs))
	}
	if msgs[0].Role != protocol.RoleUser {
		t.Errorf("msg[0] role = %s, want user", msgs[0].Role)
	}
	if msgs[1].Role != protocol.RoleAssistant || len(msgs[1].ToolCalls) != 1 {
		t.Errorf("msg[1] = %+v, want assistant with 1 tool call", msgs[1])
	}
	if msgs[2].Role != protocol.RoleTool || msgs[2].ToolCallID != "call_1" || msgs[2].Content != "file.txt" {
		t.Errorf("msg[2] = %+v, want tool call_1 with content file.txt", msgs[2])
	}
}

func TestHydrate_SkipsSystemAndIncomplete(t *testing.T) {
	ms := newFakeMessageStore()
	for _, r := range []store.MessageRecord{
		{ID: "sys", SessionID: "alpha", Role: "system", Content: "workspace event", Timestamp: 1, Done: true},
		{ID: "u1", SessionID: "alpha", Role: "user", Content: "hi", Timestamp: 2, Done: true},
		// assistant 残留行：streaming 中断，Done=false，必须跳过
		{ID: "half", SessionID: "alpha", Role: "assistant", Content: "half written", Timestamp: 3, Done: false},
		{ID: "a1", SessionID: "alpha", Role: "assistant", Content: "complete", Timestamp: 4, Done: true},
	} {
		if err := ms.Save(context.Background(), &r); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	f := factoryWithMessageStore(ms)
	sess, err := f.NewAgentLoopSession("alpha")
	if err != nil {
		t.Fatalf("NewAgentLoopSession: %v", err)
	}
	t.Cleanup(sess.Close)

	msgs := sess.Agent.Session().Messages()
	if len(msgs) != 2 {
		t.Fatalf("hydrated messages = %d, want 2 (system + incomplete skipped)", len(msgs))
	}
	if msgs[0].Role != protocol.RoleUser || msgs[1].Role != protocol.RoleAssistant {
		t.Errorf("roles = (%s, %s), want (user, assistant)", msgs[0].Role, msgs[1].Role)
	}
}

func TestHydrate_NilMessageStoreLeavesEmptySession(t *testing.T) {
	f := factoryWithMessageStore(nil)
	sess, err := f.NewAgentLoopSession("alpha")
	if err != nil {
		t.Fatalf("NewAgentLoopSession: %v", err)
	}
	t.Cleanup(sess.Close)
	if got := sess.Agent.Session().Len(); got != 0 {
		t.Fatalf("session Len = %d, want 0 (nil MessageStore no-op)", got)
	}
}

func TestHydrate_IgnoresOtherSessions(t *testing.T) {
	ms := newFakeMessageStore()
	for _, r := range []store.MessageRecord{
		{ID: "u1", SessionID: "beta", Role: "user", Content: "other session", Timestamp: 1, Done: true},
	} {
		if err := ms.Save(context.Background(), &r); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	f := factoryWithMessageStore(ms)
	sess, err := f.NewAgentLoopSession("alpha")
	if err != nil {
		t.Fatalf("NewAgentLoopSession: %v", err)
	}
	t.Cleanup(sess.Close)
	if got := sess.Agent.Session().Len(); got != 0 {
		t.Fatalf("session Len = %d, want 0 (beta rows must not leak)", got)
	}
}

func TestRecordToMessages_UnknownRoleSkipped(t *testing.T) {
	msgs, err := recordToMessages(store.MessageRecord{
		Role: "tool", Content: "orphan", Done: true,
	})
	if err != nil {
		t.Fatalf("recordToMessages: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("messages = %d, want 0", len(msgs))
	}
}

func TestRecordToMessages_InvalidToolCallsErrors(t *testing.T) {
	_, err := recordToMessages(store.MessageRecord{
		Role: "assistant", Content: "", ToolCalls: "not-json", Done: true,
	})
	if err == nil {
		t.Fatalf("expected error for malformed tool_calls")
	}
}
