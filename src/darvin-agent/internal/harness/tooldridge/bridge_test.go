package tooldridge

import (
	"testing"

	"darvin-cowork/backend/internal/agents/protocol"
)

type stubRegistry struct {
	specs []protocol.ToolSpec
	names []string
	entry *protocol.Entry
}

func (s *stubRegistry) Get(name string) protocol.Tool                { return nil }
func (s *stubRegistry) GetEntry(name string) (*protocol.Entry, bool) { return s.entry, s.entry != nil }
func (s *stubRegistry) Specs() []protocol.ToolSpec                   { return s.specs }
func (s *stubRegistry) Names() []string                              { return s.names }
func (s *stubRegistry) List() []*protocol.Entry                      { return nil }
func (s *stubRegistry) SetGrantedReads([]string)                     {}
func (s *stubRegistry) ApprovePath(string)                           {}
func (s *stubRegistry) EvaluatePermission(string, map[string]any) protocol.PermissionEval {
	return protocol.PermissionEval{}
}
func (s *stubRegistry) ScopedForSkill([]string) protocol.ToolRegistry { return s }

func TestNewBridgeSpecs(t *testing.T) {
	reg := &stubRegistry{specs: []protocol.ToolSpec{{Name: "echo"}, {Name: "read"}}}
	s := New(reg)
	got := s.Specs()
	if len(got) != 2 || got[0].Name != "echo" || got[1].Name != "read" {
		t.Fatalf("Specs = %+v", got)
	}
}

func TestNewBridgeNames(t *testing.T) {
	reg := &stubRegistry{names: []string{"a", "b", "c"}}
	s := New(reg)
	got := s.Names()
	if len(got) != 3 || got[0] != "a" {
		t.Fatalf("Names = %v", got)
	}
}

func TestNewBridgeGetEntry(t *testing.T) {
	entry := &protocol.Entry{Kind: protocol.KindBuiltIn}
	reg := &stubRegistry{entry: entry}
	s := New(reg)

	if got, ok := s.GetEntry("any"); !ok || got != entry {
		t.Fatalf("GetEntry hit = %v, %v; want the stub entry", got, ok)
	}
	regMissing := &stubRegistry{}
	if _, ok := New(regMissing).GetEntry("any"); ok {
		t.Fatal("GetEntry on missing registry returned ok=true")
	}
}

func TestBridgeWithMiddlewareLeavesOriginalUnchanged(t *testing.T) {
	reg := &stubRegistry{names: []string{"echo"}}
	base := New(reg)

	var calls int
	mw := func(r protocol.Result) protocol.Result { calls++; return r }
	augmented := base.WithMiddleware(mw)

	if len(augmented.(interface{ Names() []string }).Names()) != 1 {
		t.Fatal("Names on augmented surface missing")
	}
	// Base must not be modified.
	_ = base.ApplyMiddleware(protocol.Result{})
	if calls != 0 {
		t.Fatalf("base.ApplyMiddleware invoked the new middleware: %d", calls)
	}
}

func TestBridgeApplyMiddlewareOrder(t *testing.T) {
	reg := &stubRegistry{}
	s := New(reg)

	tag := func(want string) ResultMiddleware {
		return func(r protocol.Result) protocol.Result {
			if r.Metadata == nil {
				r.Metadata = map[string]any{}
			}
			existing, _ := r.Metadata["order"].([]string)
			r.Metadata["order"] = append(existing, want)
			return r
		}
	}
	out := s.WithMiddleware(tag("first"), tag("second"), tag("third")).ApplyMiddleware(protocol.Result{})

	got, _ := out.Metadata["order"].([]string)
	// Middleware compose right-to-left: the last appended sees the result
	// first. Reading the chain from outermost to innermost matches the
	// caller's order of WithMiddleware.
	want := []string{"third", "second", "first"}
	if len(got) != len(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestApplyNilSurfaceIsNoOp(t *testing.T) {
	r := protocol.Result{Content: "untouched"}
	if got := Apply(nil, r); got.Content != "untouched" {
		t.Fatalf("Apply(nil) = %+v, want untouched", got)
	}
}
