package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"darvin-cowork/backend/internal/agent/llm"
	"darvin-cowork/backend/internal/agent/session"
	"darvin-cowork/backend/internal/agent/store"
	"darvin-cowork/backend/internal/agent/tool"
)

// scriptedProvider is a minimal llm.ModelProvider that returns the same
// event sequence on every Stream call. Used by agent-level tests.
type scriptedProvider struct {
	mu     sync.Mutex
	events []llm.StreamEvent
}

func (s *scriptedProvider) Name() string { return "scripted" }
func (s *scriptedProvider) Complete(_ context.Context, _ *llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return nil, errors.New("not implemented")
}
func (s *scriptedProvider) Stream(_ context.Context, _ *llm.CompletionRequest) (*llm.StreamingResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan llm.StreamEvent, len(s.events)+1)
	for _, e := range s.events {
		ch <- e
	}
	close(ch)
	return llm.NewStreamingResponse(ch, nil), nil
}

func TestNewRequiresSessionAndProvider(t *testing.T) {
	if _, err := New(NewAgentConfig{}); !errors.Is(err, ErrSessionRequired) {
		t.Errorf("nil Session: err = %v, want ErrSessionRequired", err)
	}
	_, err := New(NewAgentConfig{Session: session.NewSession("x")})
	if !errors.Is(err, ErrProviderRequired) {
		t.Errorf("nil Provider: err = %v, want ErrProviderRequired", err)
	}
}

func TestNewAutoRegistersBuiltinTools(t *testing.T) {
	a, err := New(NewAgentConfig{
		Session:  session.NewSession("s"),
		Provider: &scriptedProvider{},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Registry.Names() returns sorted alphabetically.
	want := []string{"edit_file", "list_dir", "read_file", "shell", "write_file"}
	got := a.Tools().Names()
	if len(got) != len(want) {
		t.Fatalf("registered %d tools, want %d (%v)", len(got), len(want), got)
	}
	for i, n := range want {
		if got[i] != n {
			t.Errorf("tools[%d] = %q, want %q", i, got[i], n)
		}
	}
}

func TestNewRespectsCustomTools(t *testing.T) {
	custom := tool.NewRegistry()
	custom.MustRegister(&echoAdapter{})
	a, err := New(NewAgentConfig{
		Session:  session.NewSession("s"),
		Provider: &scriptedProvider{},
		Tools:    custom,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := a.Tools().Names(); len(got) != 1 || got[0] != "echo" {
		t.Errorf("Names = %v, want [echo]", got)
	}
}

func TestNewRespectsCustomStore(t *testing.T) {
	var s store.SessionStore = store.NewMemoryStore()
	if s == nil {
		t.Fatal("NewMemoryStore returned nil SessionStore")
	}
	a, err := New(NewAgentConfig{
		Session:  session.NewSession("s"),
		Provider: &scriptedProvider{},
		Store:    s,
	})
	if err != nil {
		t.Fatal(err)
	}
	// constructed with the custom store
	_ = a
}

type echoAdapter struct{}

func (echoAdapter) Name() string                    { return "echo" }
func (echoAdapter) Description() string             { return "echo" }
func (echoAdapter) Parameters() llm.ParameterSchema { return llm.ParameterSchema{Type: "object"} }
func (echoAdapter) Execute(_ context.Context, _ map[string]any) tool.Result {
	return tool.Result{Content: "ok"}
}
