package tool

import (
	"context"
	"errors"
	"testing"

	"darvin-cowork/backend/internal/agent/llm"
)

type stubTool struct{ name string }

func (s *stubTool) Name() string                    { return s.name }
func (s *stubTool) Description() string             { return "stub" }
func (s *stubTool) Parameters() llm.ParameterSchema { return llm.ParameterSchema{Type: "object"} }
func (s *stubTool) Execute(_ context.Context, _ map[string]any) Result {
	return Result{}
}

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubTool{name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	if got := r.Get("alpha"); got == nil {
		t.Error("Get(alpha) = nil, want tool")
	}
	if got := r.Get("missing"); got != nil {
		t.Errorf("Get(missing) = %v, want nil", got)
	}
}

func TestRegistryDuplicate(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&stubTool{name: "dup"})
	if err := r.Register(&stubTool{name: "dup"}); !errors.Is(err, ErrAlreadyRegistered) {
		t.Errorf("err = %v, want ErrAlreadyRegistered", err)
	}
}

func TestRegistrySpecsSorted(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&stubTool{name: "c"})
	_ = r.Register(&stubTool{name: "a"})
	_ = r.Register(&stubTool{name: "b"})
	specs := r.Specs()
	if len(specs) != 3 {
		t.Fatalf("len(specs) = %d, want 3", len(specs))
	}
	got := []string{specs[0].Name, specs[1].Name, specs[2].Name}
	want := []string{"a", "b", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("specs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRegistryNilTool(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(nil); err == nil {
		t.Error("Register(nil) should return error")
	}
}
