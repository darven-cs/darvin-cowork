// Tests for the imported-file store.

package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newTestImportedFileStore(t *testing.T) *ImportedFileStore {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "sessions.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := db.AutoMigrate(&ImportedFile{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	return NewImportedFileStore(db)
}

func rec(sessionID, relPath, sha string, size int64) ImportedFile {
	return ImportedFile{
		ID:           "id-" + relPath,
		SessionID:    sessionID,
		OriginalName: filepath.Base(relPath),
		RelativePath: relPath,
		Size:         size,
		Sha256:       sha,
	}
}

func TestImportedFileStoreInsertListSum(t *testing.T) {
	ctx := context.Background()
	s := newTestImportedFileStore(t)

	inserted, err := s.Insert(ctx, rec("s1", "spec.md", "abc", 4096))
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if inserted.ID == "" || inserted.RelativePath != "spec.md" {
		t.Errorf("inserted = %+v", inserted)
	}

	rows, err := s.List(ctx, "s1")
	if err != nil || len(rows) != 1 {
		t.Fatalf("List: rows=%v err=%v", len(rows), err)
	}

	sum, err := s.SumBytes(ctx, "s1")
	if err != nil || sum != 4096 {
		t.Errorf("SumBytes = %d, err=%v, want 4096", sum, err)
	}
}

func TestImportedFileStoreInsertDedupe(t *testing.T) {
	ctx := context.Background()
	s := newTestImportedFileStore(t)

	if _, err := s.Insert(ctx, rec("s1", "a.md", "sha1", 100)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Insert(ctx, rec("s1", "a.md", "sha1", 100)); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("second insert err = %v, want ErrDuplicate", err)
	}
	rows, err := s.List(ctx, "s1")
	if err != nil || len(rows) != 1 {
		t.Fatalf("List after dedupe: rows=%v err=%v, want 1", len(rows), err)
	}
}

func TestImportedFileStoreInsertWorkspaceFull(t *testing.T) {
	ctx := context.Background()
	s := newTestImportedFileStore(t)

	big := MaxWorkspaceBytes/2 + 1
	if _, err := s.Insert(ctx, rec("s1", "x.bin", "sha-x", big)); err != nil {
		t.Fatal(err)
	}
	// Second file crosses the cap.
	if _, err := s.Insert(ctx, rec("s1", "y.bin", "sha-y", big)); !errors.Is(err, ErrWorkspaceFull) {
		t.Fatalf("second insert err = %v, want ErrWorkspaceFull", err)
	}
	rows, _ := s.List(ctx, "s1")
	if len(rows) != 1 {
		t.Errorf("rows = %d, want 1 (the oversized second file must not be written)", len(rows))
	}
}

func TestImportedFileStoreInsertConcurrency(t *testing.T) {
	ctx := context.Background()
	s := newTestImportedFileStore(t)

	half := MaxWorkspaceBytes / 2
	if _, err := s.Insert(ctx, rec("s1", "seed.bin", "sha-seed", half)); err != nil {
		t.Fatal(err)
	}
	// Two concurrent imports of half each: exactly one may fit (total would
	// reach the cap); the second must be rejected with ErrWorkspaceFull.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = s.Insert(ctx, rec("s1", fmt.Sprintf("f%d.bin", i), fmt.Sprintf("sha-%d", i), half))
		}(i)
	}
	wg.Wait()
	var okCount, fullCount int
	for _, err := range errs {
		switch {
		case err == nil:
			okCount++
		case errors.Is(err, ErrWorkspaceFull):
			fullCount++
		}
	}
	if okCount != 1 || fullCount != 1 {
		t.Errorf("ok=%d full=%d, want 1/1", okCount, fullCount)
	}
	sum, _ := s.SumBytes(ctx, "s1")
	if sum != 2*half {
		t.Errorf("SumBytes = %d, want %d (must not exceed cap)", sum, 2*half)
	}
}

func TestImportedFileStoreDeleteBySession(t *testing.T) {
	ctx := context.Background()
	s := newTestImportedFileStore(t)

	_, _ = s.Insert(ctx, rec("s1", "a.md", "sha-a", 10))
	_, _ = s.Insert(ctx, rec("s2", "b.md", "sha-b", 10))
	if err := s.DeleteBySession(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.List(ctx, "s1")
	if len(rows) != 0 {
		t.Errorf("s1 rows = %d, want 0", len(rows))
	}
	rows2, _ := s.List(ctx, "s2")
	if len(rows2) != 1 {
		t.Errorf("s2 rows = %d, want 1 (other session untouched)", len(rows2))
	}
}

func TestImportedFileStoreDeleteOne(t *testing.T) {
	ctx := context.Background()
	s := newTestImportedFileStore(t)

	_, _ = s.Insert(ctx, rec("s1", "a.md", "sha-a", 10))
	if err := s.Delete(ctx, "s1", "a.md"); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.List(ctx, "s1")
	if len(rows) != 0 {
		t.Errorf("rows = %d, want 0", len(rows))
	}
}
