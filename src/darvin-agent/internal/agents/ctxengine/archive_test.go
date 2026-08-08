package ctxengine

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"darvin-cowork/backend/internal/agents/protocol"
)

func TestFileArchiver_DisabledWhenDirEmpty(t *testing.T) {
	a := NewFileArchiver("", nil)
	path, err := a.Archive(context.Background(), []protocol.Message{{Role: protocol.RoleUser, Content: "hi"}})
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if path != "" {
		t.Errorf("path = %q, want empty", path)
	}
}

func TestFileArchiver_WritesJsonl(t *testing.T) {
	dir := t.TempDir()
	a := NewFileArchiver(dir, nil)
	msgs := []protocol.Message{
		{Role: protocol.RoleUser, Content: "u1", ID: "u1", Timestamp: 100},
		{Role: protocol.RoleAssistant, Content: "a1", ID: "a1", Timestamp: 200},
	}

	path, err := a.Archive(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if path == "" {
		t.Fatal("path empty")
	}
	if !strings.HasSuffix(path, ".jsonl") {
		t.Errorf("path = %q, want .jsonl suffix", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	var decoded []protocol.Message
	for sc.Scan() {
		var m protocol.Message
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("unmarshal line %q: %v", sc.Text(), err)
		}
		decoded = append(decoded, m)
	}
	if len(decoded) != len(msgs) {
		t.Fatalf("decoded %d, want %d", len(decoded), len(msgs))
	}
	for i := range msgs {
		if decoded[i].Content != msgs[i].Content {
			t.Errorf("decoded[%d].Content = %q, want %q", i, decoded[i].Content, msgs[i].Content)
		}
		if decoded[i].ID != msgs[i].ID {
			t.Errorf("decoded[%d].ID = %q, want %q", i, decoded[i].ID, msgs[i].ID)
		}
	}
}

func TestFileArchiver_ConcurrentWritesDistinctFiles(t *testing.T) {
	dir := t.TempDir()
	a := NewFileArchiver(dir, nil)

	var wg sync.WaitGroup
	const n = 8
	paths := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p, err := a.Archive(context.Background(), []protocol.Message{
				{Role: protocol.RoleUser, Content: "go" + string(rune('a'+i))},
			})
			paths[i] = p
			errs[i] = err
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
		if paths[i] == "" {
			t.Errorf("goroutine %d: empty path", i)
		}
	}
	seen := map[string]struct{}{}
	for _, p := range paths {
		if _, dup := seen[p]; dup {
			t.Errorf("duplicate path %q under concurrent writes", p)
		}
		seen[p] = struct{}{}
	}
	if len(seen) != n {
		t.Errorf("got %d distinct paths, want %d", len(seen), n)
	}
}

func TestFileArchiver_FailureEmitsNotice(t *testing.T) {
	var gotText, gotDetail string
	a := NewFileArchiver("/nonexistent-readonly-path-xyz", func(text, detail string) {
		gotText = text
		gotDetail = detail
	})
	_, err := a.Archive(context.Background(), []protocol.Message{{Role: protocol.RoleUser, Content: "x"}})
	if err == nil {
		t.Fatal("expected error on read-only dir")
	}
	if gotText == "" {
		t.Errorf("emit never called; text=%q", gotText)
	}
	if !strings.Contains(gotDetail, err.Error()) {
		t.Errorf("detail = %q, want to contain %q", gotDetail, err.Error())
	}
}

func TestFileArchiver_CancelledContext(t *testing.T) {
	dir := t.TempDir()
	a := NewFileArchiver(dir, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := a.Archive(ctx, []protocol.Message{{Role: protocol.RoleUser, Content: "x"}})
	if err == nil {
		t.Errorf("expected error on cancelled context")
	}
}

func TestFileArchiver_CreatesMissingDir(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "nested", "archive")
	a := NewFileArchiver(dir, nil)
	path, err := a.Archive(context.Background(), []protocol.Message{{Role: protocol.RoleUser, Content: "x"}})
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if !strings.HasPrefix(path, dir) {
		t.Errorf("path = %q, want prefix %q", path, dir)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not present: %v", err)
	}
}