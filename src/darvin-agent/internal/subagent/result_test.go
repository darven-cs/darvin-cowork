// Tests for Paginate.

package subagent

import (
	"strings"
	"testing"
)

func TestPaginate_DefaultLimit(t *testing.T) {
	big := strings.Repeat("a", 30*1024)
	got := Paginate(big, 0, 0)
	if len(got) != DefaultPageSize {
		t.Fatalf("want default page size %d, got %d", DefaultPageSize, len(got))
	}
}

func TestPaginate_ClampOverMax(t *testing.T) {
	big := strings.Repeat("a", 30*1024)
	got := Paginate(big, 0, 100*1024)
	if len(got) != MaxPageSize {
		t.Fatalf("want max page size %d, got %d", MaxPageSize, len(got))
	}
}

func TestPaginate_OffsetWindow(t *testing.T) {
	text := "abcdefghij"
	got := Paginate(text, 3, 2)
	if got != "de" {
		t.Fatalf("want 'de', got %q", got)
	}
}

func TestPaginate_OffsetPastEnd(t *testing.T) {
	got := Paginate("abc", 10, 5)
	if got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestPaginate_NegativeOffset(t *testing.T) {
	got := Paginate("abc", -5, 2)
	if got != "ab" {
		t.Fatalf("want negative offset clamped to 0, got %q", got)
	}
}

func TestRunResult_AppendAndTruncate(t *testing.T) {
	r := &RunResult{}
	r.Append([]byte("hello"), 8)
	if r.Snapshot() != "hello" {
		t.Fatalf("want 'hello', got %q", r.Snapshot())
	}
	if r.Truncated() {
		t.Fatalf("want not truncated at 5 bytes < cap 8")
	}
	r.Append([]byte(" world"), 8)
	if !r.Truncated() {
		t.Fatalf("want truncated after over-cap")
	}
	if r.Snapshot() != "hello wo" {
		t.Fatalf("want cap-truncated 'hello wo', got %q", r.Snapshot())
	}
}
