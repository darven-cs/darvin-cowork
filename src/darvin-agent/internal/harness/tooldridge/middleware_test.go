package tooldridge

import (
	"strings"
	"testing"

	"darvin-cowork/backend/internal/agents/protocol"
)

func TestMaxResultBytesTruncates(t *testing.T) {
	mw := MaxResultBytes(8)
	in := protocol.Result{Content: strings.Repeat("a", 100)}
	out := mw(in)

	if len(out.Content) > 8+len("[truncated 92 bytes]") {
		t.Fatalf("Content length = %d, want ≤ %d", len(out.Content), 8+len("[truncated 92 bytes]"))
	}
	if !strings.Contains(out.Content, "[truncated") {
		t.Fatalf("Content missing marker: %q", out.Content)
	}
	if got := out.Metadata["truncated"]; got != 92 {
		t.Fatalf("truncated = %v, want 92", got)
	}
}

func TestMaxResultBytesUnderLimit(t *testing.T) {
	mw := MaxResultBytes(1000)
	in := protocol.Result{Content: "short"}
	if got := mw(in); got.Content != "short" {
		t.Fatalf("Content = %q, want short", got.Content)
	}
}

func TestMaxResultLinesTruncates(t *testing.T) {
	mw := MaxResultLines(3)
	in := protocol.Result{Content: "a\nb\nc\nd\ne"}
	out := mw(in)

	if strings.Count(out.Content, "\n") >= 4 {
		t.Fatalf("Content kept too many lines: %q", out.Content)
	}
	if got := out.Metadata["line_truncated"]; got != 2 {
		t.Fatalf("line_truncated = %v, want 2", got)
	}
}

func TestMaxResultLinesUnderLimit(t *testing.T) {
	mw := MaxResultLines(100)
	in := protocol.Result{Content: "a\nb\nc"}
	if got := mw(in); got.Content != "a\nb\nc" {
		t.Fatalf("Content = %q, want untouched", got.Content)
	}
}

func TestNormalizeErrorPlainText(t *testing.T) {
	mw := NormalizeError()
	in := protocol.Result{Content: "error: connection refused"}
	out := mw(in)

	if !out.IsError {
		t.Fatal("IsError = false, want true after NormalizeError")
	}
	if !strings.HasPrefix(out.Content, "[error]") {
		t.Fatalf("Content = %q, want [error] prefix", out.Content)
	}
}

func TestNormalizeErrorAlreadyError(t *testing.T) {
	mw := NormalizeError()
	in := protocol.Result{Content: "already flagged", IsError: true}
	out := mw(in)

	if !out.IsError {
		t.Fatal("IsError flipped to false")
	}
	if out.Content != "already flagged" {
		t.Fatalf("Content = %q, want untouched", out.Content)
	}
}

func TestNormalizeErrorPlainContent(t *testing.T) {
	mw := NormalizeError()
	in := protocol.Result{Content: "the answer is 42"}
	if got := mw(in); got.IsError {
		t.Fatal("IsError = true on benign content")
	}
}

func TestSanitizeControlChars(t *testing.T) {
	mw := SanitizeControlChars()
	in := protocol.Result{Content: "good\x00bad\x07text\twith\nnewlines"}
	out := mw(in)

	for _, c := range out.Content {
		if isDangerousControl(c) {
			t.Fatalf("Content still contains dangerous control char: %q", out.Content)
		}
	}
	if !strings.Contains(out.Content, "good") || !strings.Contains(out.Content, "text") {
		t.Fatalf("safe bytes lost: %q", out.Content)
	}
}

func TestSanitizeControlCharsNoChange(t *testing.T) {
	mw := SanitizeControlChars()
	in := protocol.Result{Content: "harmless\ntext\twith\ttabs"}
	if got := mw(in); got.Content != in.Content {
		t.Fatalf("Content mutated: got %q, want %q", got.Content, in.Content)
	}
}

func TestWithToolMetadata(t *testing.T) {
	mw := WithToolMetadata("bash", protocol.KindBuiltIn)
	out := mw(protocol.Result{Content: "ok"})

	if got := out.Metadata["tool_name"]; got != "bash" {
		t.Fatalf("tool_name = %v, want bash", got)
	}
	if got := out.Metadata["tool_kind"]; got != "builtin" {
		t.Fatalf("tool_kind = %v, want builtin", got)
	}
}

func TestChainComposes(t *testing.T) {
	tag := func(name string) ResultMiddleware {
		return func(r protocol.Result) protocol.Result {
			if r.Metadata == nil {
				r.Metadata = map[string]any{}
			}
			existing, _ := r.Metadata["chain"].([]string)
			r.Metadata["chain"] = append(existing, name)
			return r
		}
	}
	out := Chain(tag("a"), tag("b"), tag("c"))(protocol.Result{})

	got, _ := out.Metadata["chain"].([]string)
	// Same right-to-left composition as the bridge: the last in the chain
	// runs first.
	want := []string{"c", "b", "a"}
	if len(got) != len(want) {
		t.Fatalf("chain = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chain[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDefaultMiddlewareRunsAndPassesOn(t *testing.T) {
	chain := DefaultMiddleware()
	in := protocol.Result{Content: "harmless output"}
	out := protocol.Result{Content: in.Content}
	for _, mw := range chain {
		out = mw(out)
	}
	if out.Content != in.Content {
		t.Fatalf("DefaultMiddleware mutated a clean input: %q", out.Content)
	}
}

func TestDefaultMiddlewareCatchesErrorPrefix(t *testing.T) {
	chain := DefaultMiddleware()
	in := protocol.Result{Content: "Error: oops"}
	out := protocol.Result{Content: in.Content}
	for _, mw := range chain {
		out = mw(out)
	}
	if !out.IsError {
		t.Fatal("DefaultMiddleware did not flip IsError on an Error: prefix")
	}
}
