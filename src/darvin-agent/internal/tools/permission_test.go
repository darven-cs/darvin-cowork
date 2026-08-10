// Tests for tool permission classification.

package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestClassifyPermission(t *testing.T) {
	cases := []struct {
		name     string
		tool     string
		args     map[string]any
		wantLvl  string
		wantNeed bool
	}{
		{"rm-rf", "shell", map[string]any{"command": "rm", "args": []any{"-rf", "node_modules"}}, "destructive", true},
		{"git-push-force", "shell", map[string]any{"command": "git", "args": []any{"push", "--force", "origin", "main"}}, "destructive", true},
		{"git-reset-hard", "shell", map[string]any{"command": "git", "args": []any{"reset", "--hard"}}, "destructive", true},
		{"single-rm", "shell", map[string]any{"command": "rm", "args": []any{"notes.txt"}}, "caution", true},
		{"git-push", "shell", map[string]any{"command": "git", "args": []any{"push"}}, "caution", true},
		{"ls-safe", "shell", map[string]any{"command": "ls", "args": []any{"-la"}}, "safe", false},
		{"write-file-safe", "write_file", map[string]any{"path": "a.txt", "content": "hi"}, "safe", false},
	}
	for _, c := range cases {
		lvl, _, need := ClassifyPermission(c.tool, c.args)
		if lvl != c.wantLvl || need != c.wantNeed {
			t.Errorf("%s: ClassifyPermission = (%q, need=%v), want (%q, need=%v)", c.name, lvl, need, c.wantLvl, c.wantNeed)
		}
	}
}

func TestEvaluatePermission_GrantedReadOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "attached.txt")
	if err := os.WriteFile(outside, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	sb, err := newFsSandbox(root)
	if err != nil {
		t.Fatal(err)
	}
	sb.setGrantedReads([]string{outside})
	r := NewRegistry()
	r.sb = sb

	// attached path → authorized, no approval needed
	eval := r.EvaluatePermission("read_file", map[string]any{"path": outside})
	if !eval.Authorized || eval.Need {
		t.Errorf("granted read eval = %+v, want authorized, no need", eval)
	}
	// un-attached outside path → needs approval
	eval = r.EvaluatePermission("read_file", map[string]any{"path": "/etc/hosts"})
	if eval.Authorized || !eval.Need {
		t.Errorf("unauthorized read eval = %+v, want !authorized, need", eval)
	}
	// workspace-relative read stays authorized
	in := filepath.Join(root, "in.txt")
	if err := os.WriteFile(in, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	eval = r.EvaluatePermission("read_file", map[string]any{"path": "in.txt"})
	if !eval.Authorized || eval.Need {
		t.Errorf("in-workspace read eval = %+v, want authorized, no need", eval)
	}
}

func TestApprovedPathAllowsOutOfRootRead(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "etc-hosts.txt")
	if err := os.WriteFile(outside, []byte("host data"), 0o644); err != nil {
		t.Fatal(err)
	}
	sb, err := newFsSandbox(root)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	r.sb = sb

	// Unauthorized: out-of-sandbox reads require approval.
	eval := r.EvaluatePermission("read_file", map[string]any{"path": outside})
	if !eval.Need || eval.EscapedPath == "" {
		t.Fatalf("unauthorized read eval = %+v, want Need + EscapedPath", eval)
	}
	// After user approval: the path enters the one-shot authorised set
	// and the read can actually proceed.
	r.ApprovePath(outside)
	r.MustRegister(&readFileTool{sb: sb})
	res := r.Get("read_file").Execute(context.Background(), map[string]any{"path": outside})
	if res.IsError {
		t.Fatalf("read approved path: %v", res.Content)
	}
	if res.Content != "host data" {
		t.Errorf("content = %q, want %q", res.Content, "host data")
	}
}

func TestReadFileGrantedAttachment(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "attached.txt")
	if err := os.WriteFile(outside, []byte("hello attachment"), 0o644); err != nil {
		t.Fatal(err)
	}
	sb, err := newFsSandbox(root)
	if err != nil {
		t.Fatal(err)
	}
	sb.setGrantedReads([]string{outside})
	r := NewRegistry()
	r.MustRegister(&readFileTool{sb: sb})

	res := r.Get("read_file").Execute(context.Background(), map[string]any{"path": outside})
	if res.IsError {
		t.Fatalf("read granted attachment: %v", res.Content)
	}
	if res.Content != "hello attachment" {
		t.Errorf("content = %q, want %q", res.Content, "hello attachment")
	}
}

// stubDangerTool is a Tool that self-classifies via DangerClassifier.
type stubDangerTool struct {
	level  string
	reason string
	need   bool
}

func (t *stubDangerTool) Name() string        { return "mcp__srv__danger" }
func (t *stubDangerTool) Description() string { return "danger tool" }
func (t *stubDangerTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (t *stubDangerTool) Execute(context.Context, map[string]any) Result {
	return Result{Content: "done"}
}
func (t *stubDangerTool) ClassifyDanger(map[string]any) (string, string, bool) {
	return t.level, t.reason, t.need
}

func TestEvaluatePermissionConsultsDangerClassifier(t *testing.T) {
	r := NewRegistry()
	if err := r.RegisterTool(&stubDangerTool{level: "destructive", need: true}, KindMcp, map[string]any{
		"pluginID":    "mcp",
		"mcpServerID": "srv",
	}); err != nil {
		t.Fatal(err)
	}
	// DangerClassifier with need=true → approval required, level preserved.
	eval := r.EvaluatePermission("mcp__srv__danger", map[string]any{})
	if !eval.Need {
		t.Errorf("eval = %+v, want Need=true for classifier tool", eval)
	}
	if eval.Level != "destructive" {
		t.Errorf("Level = %q, want destructive", eval.Level)
	}
	if eval.Authorized != true {
		t.Errorf("Authorized = %v, want true (approval allowed, not hard-blocked)", eval.Authorized)
	}

	// A classifier that reports safe passes through with no approval.
	r2 := NewRegistry()
	if err := r2.RegisterTool(&stubDangerTool{level: "safe", need: false}, KindMcp, nil); err != nil {
		t.Fatal(err)
	}
	eval = r2.EvaluatePermission("mcp__srv__danger", map[string]any{})
	if eval.Need {
		t.Errorf("safe classifier eval = %+v, want Need=false", eval)
	}
}
