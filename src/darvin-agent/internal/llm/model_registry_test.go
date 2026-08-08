// Tests for the model registry lookup, listing, and concurrency safety.

package llm_test

import (
	"strconv"
	"testing"

	"darvin-cowork/backend/internal/llm"
)

func newTestModel(id, provider string) llm.ModelDescriptor {
	return llm.ModelDescriptor{
		ID: id, Name: id, Provider: provider,
		APIVersion:    llm.APIAnthropicMessages,
		ContextWindow: 100000, MaxTokens: 8192,
	}
}

func TestModelRegistry_RegisterAndGet(t *testing.T) {
	r := llm.NewModelRegistry()
	r.MustRegisterModel(newTestModel("m-1", "test"))

	got, ok := r.Get("m-1")
	if !ok {
		t.Fatal("Get(m-1) returned not-found")
	}
	if got.ID != "m-1" {
		t.Errorf("ID = %q, want %q", got.ID, "m-1")
	}
}

func TestModelRegistry_Get_Missing(t *testing.T) {
	r := llm.NewModelRegistry()
	if _, ok := r.Get("nope"); ok {
		t.Errorf("Get(nope) should return false")
	}
}

func TestModelRegistry_DuplicatePanics(t *testing.T) {
	r := llm.NewModelRegistry()
	r.MustRegisterModel(newTestModel("dup", "test"))

	defer func() {
		if rec := recover(); rec == nil {
			t.Errorf("expected panic on duplicate ID")
		}
	}()
	r.MustRegisterModel(newTestModel("dup", "test"))
}

func TestModelRegistry_ListByProvider(t *testing.T) {
	r := llm.NewModelRegistry()
	r.MustRegisterModel(newTestModel("a-1", "alpha"))
	r.MustRegisterModel(newTestModel("a-2", "alpha"))
	r.MustRegisterModel(newTestModel("b-1", "beta"))

	alpha := r.ListByProvider("alpha")
	if len(alpha) != 2 {
		t.Errorf("alpha len = %d, want 2", len(alpha))
	}

	if got := r.ListByProvider("none"); got != nil {
		t.Errorf("unknown provider should return nil, got %v", got)
	}
}

func TestModelRegistry_All(t *testing.T) {
	r := llm.NewModelRegistry()
	r.MustRegisterModel(newTestModel("a-1", "alpha"))
	r.MustRegisterModel(newTestModel("b-1", "beta"))
	r.MustRegisterModel(newTestModel("c-1", "gamma"))

	if got := r.All(); len(got) != 3 {
		t.Errorf("All() len = %d, want 3", len(got))
	}
}

func TestModelRegistry_Concurrency(t *testing.T) {
	r := llm.NewModelRegistry()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			_, _ = r.Get("missing")
			_ = r.ListByProvider("none")
		}
		close(done)
	}()
	for i := 0; i < 100; i++ {
		r.MustRegisterModel(newTestModel("k-"+strconv.Itoa(i), "p"))
	}
	<-done
}

func TestDefaultModelRegistry_IncludesAnthropicModels(t *testing.T) {
	all := llm.DefaultModelRegistry.All()
	if len(all) == 0 {
		t.Fatal("DefaultModelRegistry should have models from anthropic init()")
	}
	found := false
	for _, m := range all {
		if m.Provider == "anthropic" && m.ID == "claude-sonnet-4-5" {
			found = true
			if m.ContextWindow <= 0 {
				t.Errorf("ContextWindow = %d, want > 0", m.ContextWindow)
			}
		}
	}
	if !found {
		t.Errorf("expected claude-sonnet-4-5 in DefaultModelRegistry")
	}
}
