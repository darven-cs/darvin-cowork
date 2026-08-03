package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newShellToolForTest(t *testing.T) (Tool, string) {
	t.Helper()
	root := t.TempDir()
	sb, err := newFsSandbox(root)
	if err != nil {
		t.Fatal(err)
	}
	return newShellTool(sb, nil), root
}

func TestShellAllowlistEcho(t *testing.T) {
	sh, _ := newShellToolForTest(t)
	res := sh.Execute(context.Background(), map[string]any{
		"command": "echo",
		"args":    []any{"hello"},
	})
	if res.IsError {
		t.Fatalf("echo: %v", res.Content)
	}
	if !strings.Contains(res.Content, "hello") {
		t.Errorf("echo output missing 'hello': %q", res.Content)
	}
}

func TestShellRejectNotAllowed(t *testing.T) {
	sh, _ := newShellToolForTest(t)
	res := sh.Execute(context.Background(), map[string]any{
		"command": "curl",
		"args":    []any{"http://example.com"},
	})
	if !res.IsError {
		t.Errorf("curl should be rejected (not in allowlist), got %+v", res)
	}
	if !strings.Contains(res.Content, "must be one of") {
		t.Errorf("err should reject via command enum: %q", res.Content)
	}
}

func TestShellCwdEscape(t *testing.T) {
	sh, _ := newShellToolForTest(t)
	res := sh.Execute(context.Background(), map[string]any{
		"command": "ls",
		"args":    []any{},
		"cwd":     "/etc",
	})
	if !res.IsError {
		t.Errorf("cwd outside sandbox should error, got %+v", res)
	}
}

func TestShellTimeout(t *testing.T) {
	if _, err := lookPathOrSkip("sleep"); err != nil {
		t.Skip("sleep not available")
	}
	sh, _ := newShellToolForTest(t)
	res := sh.Execute(context.Background(), map[string]any{
		"command":    "sleep",
		"args":       []any{"5"},
		"timeout_ms": 100,
	})
	if !res.IsError {
		t.Errorf("sleep 5s with 100ms timeout should error, got %+v", res)
	}
	// ensure total wall time respects the timeout
	start := time.Now()
	_ = sh.Execute(context.Background(), map[string]any{
		"command":    "sleep",
		"args":       []any{"2"},
		"timeout_ms": 200,
	})
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Errorf("timeout took too long: %v", elapsed)
	}
}

func TestShellCustomCwd(t *testing.T) {
	sh, root := newShellToolForTest(t)
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := sh.Execute(context.Background(), map[string]any{
		"command": "pwd",
		"args":    []any{},
		"cwd":     "sub",
	})
	if res.IsError {
		t.Fatalf("pwd in sub: %v", res.Content)
	}
	if !strings.HasSuffix(strings.TrimSpace(res.Content), "/sub") {
		t.Errorf("pwd output = %q, want ending with /sub", res.Content)
	}
}

func TestShellEnumRejectCommand(t *testing.T) {
	sh, _ := newShellToolForTest(t)
	res := sh.Execute(context.Background(), map[string]any{
		"command": "rm -rf /",
		"args":    []any{},
	})
	if !res.IsError {
		t.Error("rm -rf / should be rejected by command enum")
	}
	if !strings.Contains(res.Content, "must be one of") {
		t.Errorf("err should mention enum: %q", res.Content)
	}
}

func TestShellOutputTruncation(t *testing.T) {
	sh, root := newShellToolForTest(t)
	if _, err := lookPathOrSkip("cat"); err != nil {
		t.Skip("cat not available")
	}
	// 2 MiB of output; cap at 64 KiB via max_output_bytes.
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte(strings.Repeat("y", 2<<20)), 0o644); err != nil {
		t.Fatal(err)
	}
	res := sh.Execute(context.Background(), map[string]any{
		"command":          "cat",
		"args":             []any{"big.txt"},
		"max_output_bytes": float64(65536),
	})
	if res.IsError {
		t.Fatalf("cat big: %v", res.Content)
	}
	if !strings.Contains(res.Content, "[stdout truncated at 65536 bytes]") {
		t.Errorf("truncation note missing: %q", res.Content[:80])
	}
	if len(res.Content) > 1<<20 {
		t.Errorf("truncated output still too large: %d bytes", len(res.Content))
	}
}

func TestShellMaxOutputBytesZeroUsesDefault(t *testing.T) {
	sh, _ := newShellToolForTest(t)
	if _, err := lookPathOrSkip("echo"); err != nil {
		t.Skip("echo not available")
	}
	res := sh.Execute(context.Background(), map[string]any{
		"command":          "echo",
		"args":             []any{"hi"},
		"max_output_bytes": float64(0),
	})
	if res.IsError {
		t.Fatalf("echo with max_output_bytes=0: %v", res.Content)
	}
	if !strings.Contains(res.Content, "hi") {
		t.Errorf("echo output missing: %q", res.Content)
	}
}

func lookPathOrSkip(name string) (string, error) {
	// best-effort, ignores PATH: rely on /usr/bin/<name> or PATH
	for _, p := range []string{"/usr/bin/" + name, "/bin/" + name} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", os.ErrNotExist
}
