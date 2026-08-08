package ctxengine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"darvin-cowork/backend/internal/agents/protocol"
)

// Archiver persists the original messages dropped during a compaction
// so the full history stays traceable for debug / replay. Implementations
// MUST be best-effort: any write failure returns the path so far and an
// error, but the caller treats archive failure as non-fatal (the live
// compaction has already produced a digest and replaced the session).
//
// Returning "" with a nil error means "archive disabled" (no dir
// configured); the caller skips the write path entirely.
type Archiver interface {
	Archive(ctx context.Context, msgs []protocol.Message) (path string, err error)
}

// FileArchiver writes one timestamped jsonl per Archive call. The
// filename format follows the jsonl convention so external tooling can
// ingest the files without a custom parser.
//
// The directory is created lazily on first use; an empty dir disables
// archive (the most common configuration in fresh installs).
type FileArchiver struct {
	mu   sync.Mutex
	dir  string
	emit func(noticeText, detail string)
}

// NewFileArchiver wires a FileArchiver. emit may be nil for tests that
// do not want Notice plumbing; production callers pass
// agent.Agent.Emit wrapped to match the (text, detail) signature.
func NewFileArchiver(dir string, emit func(text, detail string)) *FileArchiver {
	return &FileArchiver{dir: dir, emit: emit}
}

// Archive serialises msgs as one JSON object per line and writes the
// result to <dir>/<YYYYMMDD-HHMMSS.NNN>.jsonl. The returned path is
// stable for the duration of this call and safe to embed in the digest
// body so the model can point the user at it.
//
// Errors propagate verbatim; callers should treat any non-nil error as
// "archive failed, compaction still succeeded". The mutex guards the
// concurrent-write boundary so two parallel Archive calls land in
// distinct files even when the wall clock collides on the second.
func (a *FileArchiver) Archive(ctx context.Context, msgs []protocol.Message) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if a.dir == "" {
		return "", nil
	}
	if err := os.MkdirAll(a.dir, 0o755); err != nil {
		a.fail("archive mkdir failed", err)
		return "", err
	}
	name := time.Now().Format("20060102-150405.000000") + ".jsonl"
	path := filepath.Join(a.dir, name)

	a.mu.Lock()
	defer a.mu.Unlock()

	f, err := os.Create(path)
	if err != nil {
		a.fail("archive create failed", err)
		return "", err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, m := range msgs {
		if err := enc.Encode(m); err != nil {
			a.fail(fmt.Sprintf("archive encode failed at %s", path), err)
			return path, err
		}
	}
	return path, nil
}

func (a *FileArchiver) fail(text string, err error) {
	if a.emit == nil {
		return
	}
	a.emit(text, err.Error())
}
