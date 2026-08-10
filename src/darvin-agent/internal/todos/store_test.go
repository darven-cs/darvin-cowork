// Tests for the host-side task list store.

package todos

import (
	"strings"
	"testing"
)

func TestSetGetCopies(t *testing.T) {
	Clear("s1")
	src := []Item{{Content: "A", Status: "pending", Level: 0}}
	Set("s1", src)
	got, ok := Get("s1")
	if !ok {
		t.Fatal("expected an entry")
	}
	if len(got) != 1 || got[0].Content != "A" {
		t.Fatalf("Get = %+v, want [A]", got)
	}
	// Mutating the caller's slice must not leak into the store.
	src[0].Content = "B"
	got, _ = Get("s1")
	if got[0].Content != "A" {
		t.Errorf("store mutated by caller: %q", got[0].Content)
	}
	Clear("s1")
}

func TestSetEmptyClears(t *testing.T) {
	Set("s1", []Item{{Content: "A", Status: "pending"}})
	Set("s1", nil)
	if _, ok := Get("s1"); ok {
		t.Error("empty list should clear the entry")
	}
}

func TestClear(t *testing.T) {
	Set("s1", []Item{{Content: "A", Status: "pending"}})
	Clear("s1")
	if _, ok := Get("s1"); ok {
		t.Error("expected no entry after Clear")
	}
}

func TestBlockRendersList(t *testing.T) {
	Set("s1", []Item{
		{Content: "Phase A", Status: "in_progress", Level: 0},
		{Content: "Sub B", Status: "pending", Level: 1},
	})
	defer Clear("s1")
	block := Block("s1")
	for _, want := range []string{
		"<active-todos>",
		"- [in_progress] Phase A",
		"  - [pending] Sub B",
		"</active-todos>",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("block missing %q:\n%s", want, block)
		}
	}
}

func TestBlockEmpty(t *testing.T) {
	Clear("s1")
	if Block("s1") != "" {
		t.Error("expected empty block with no list")
	}
	Set("s1", nil)
	if Block("s1") != "" {
		t.Error("expected empty block with empty list")
	}
	Clear("s1")
}

func TestParseArgs(t *testing.T) {
	items, ok := ParseArgs(map[string]any{
		"todos": []any{
			map[string]any{"content": "Phase", "status": "in_progress", "activeForm": "Doing"},
			map[string]any{"content": "Sub", "status": "pending", "level": float64(1)},
		},
	})
	if !ok {
		t.Fatal("expected valid parse")
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[0].Status != "in_progress" || items[0].ActiveForm != "Doing" || items[0].Level != 0 {
		t.Errorf("items[0] = %+v", items[0])
	}
	if items[1].Level != 1 {
		t.Errorf("items[1].Level = %d, want 1", items[1].Level)
	}
}

func TestParseArgsEmptyList(t *testing.T) {
	items, ok := ParseArgs(map[string]any{"todos": []any{}})
	if !ok || len(items) != 0 {
		t.Errorf("empty list should parse ok with zero items, got %v %v", items, ok)
	}
}

func TestParseArgsInvalid(t *testing.T) {
	if _, ok := ParseArgs(map[string]any{}); ok {
		t.Error("missing todos should be invalid")
	}
	if _, ok := ParseArgs(map[string]any{"todos": "nope"}); ok {
		t.Error("non-array todos should be invalid")
	}
}
