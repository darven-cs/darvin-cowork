package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newTestDigestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := gorm.Open(sqlite.Open(filepath.Join(dir, "test.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := db.AutoMigrate(&SessionDigest{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	return db
}

func sampleDigest(id, sessionID string, sequence int) *SessionDigest {
	return &SessionDigest{
		ID:              id,
		SessionID:       sessionID,
		Sequence:        sequence,
		Summary:         "summary " + id,
		TokensBefore:    1000,
		TokensAfter:     400,
		FirstKeptID:     "msg-1",
		FirstKeptTimestamp: 123456,
		CompactReason:   "budget_exceeded",
		SourceCompactID: "cp-1",
		CreatedAt:       1700000000000,
	}
}

func TestSQLiteDigestStoreSaveAllocatesSequence(t *testing.T) {
	db := newTestDigestDB(t)
	s := NewSQLiteDigestStore(db)
	ctx := context.Background()

	d1 := sampleDigest("digest-cp-1", "s1", 0)
	if err := s.Save(ctx, d1); err != nil {
		t.Fatalf("Save #1: %v", err)
	}
	if d1.Sequence != 1 {
		t.Fatalf("Sequence after first save = %d, want 1", d1.Sequence)
	}

	d2 := sampleDigest("digest-cp-2", "s1", 0)
	if err := s.Save(ctx, d2); err != nil {
		t.Fatalf("Save #2: %v", err)
	}
	if d2.Sequence != 2 {
		t.Fatalf("Sequence after second save = %d, want 2", d2.Sequence)
	}
}

func TestSQLiteDigestStoreListAndLatest(t *testing.T) {
	db := newTestDigestDB(t)
	s := NewSQLiteDigestStore(db)
	ctx := context.Background()

	for i, id := range []string{"digest-cp-1", "digest-cp-2", "digest-cp-3"} {
		if err := s.Save(ctx, sampleDigest(id, "s1", 0)); err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}

	rows, err := s.List(ctx, "s1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("len(rows) = %d, want 3", len(rows))
	}
	for i, r := range rows {
		if r.Sequence != i+1 {
			t.Errorf("rows[%d].Sequence = %d, want %d", i, r.Sequence, i+1)
		}
	}

	latest, err := s.Latest(ctx, "s1")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if latest == nil || latest.Sequence != 3 {
		t.Fatalf("latest = %+v, want Sequence=3", latest)
	}

	none, err := s.Latest(ctx, "missing")
	if err != nil {
		t.Fatalf("Latest missing: %v", err)
	}
	if none != nil {
		t.Fatalf("latest missing session = %+v, want nil", none)
	}
}

func TestSQLiteDigestStoreDeleteBySession(t *testing.T) {
	db := newTestDigestDB(t)
	s := NewSQLiteDigestStore(db)
	ctx := context.Background()

	_ = s.Save(ctx, sampleDigest("digest-cp-1", "s1", 0))
	_ = s.Save(ctx, sampleDigest("digest-cp-2", "s1", 0))
	if err := s.DeleteBySession(ctx, "s1"); err != nil {
		t.Fatalf("DeleteBySession: %v", err)
	}

	rows, _ := s.List(ctx, "s1")
	if len(rows) != 0 {
		t.Fatalf("after delete: %d rows, want 0", len(rows))
	}

	// Cache must have been cleared — next Save starts at 1 again.
	d := sampleDigest("digest-cp-3", "s1", 0)
	if err := s.Save(ctx, d); err != nil {
		t.Fatalf("Save after delete: %v", err)
	}
	if d.Sequence != 1 {
		t.Fatalf("Sequence after delete+save = %d, want 1", d.Sequence)
	}
}

func TestSQLiteDigestStoreHonoursExplicitSequence(t *testing.T) {
	db := newTestDigestDB(t)
	s := NewSQLiteDigestStore(db)
	ctx := context.Background()

	d := sampleDigest("digest-cp-1", "s1", 42)
	if err := s.Save(ctx, d); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if d.Sequence != 42 {
		t.Fatalf("Sequence = %d, want 42 (caller-supplied)", d.Sequence)
	}
}

func TestSQLiteDigestStoreConcurrentSaves(t *testing.T) {
	db := newTestDigestDB(t)
	s := NewSQLiteDigestStore(db)
	ctx := context.Background()

	const N = 100
	var wg sync.WaitGroup
	wg.Add(N)
	seen := make([]int, N)
	var mu sync.Mutex
	var firstErr error

	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			d := sampleDigest("digest-cp-"+idFor(i), "s1", 0)
			if err := s.Save(ctx, d); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			mu.Lock()
			seen[i] = d.Sequence
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	if firstErr != nil {
		t.Fatalf("concurrent save failed: %v", firstErr)
	}

	seenSeq := map[int]bool{}
	for i, seq := range seen {
		if seq <= 0 {
			t.Fatalf("goroutine %d got Sequence=%d", i, seq)
		}
		if seenSeq[seq] {
			t.Fatalf("duplicate sequence %d", seq)
		}
		seenSeq[seq] = true
	}

	rows, _ := s.List(ctx, "s1")
	if len(rows) != N {
		t.Fatalf("List returned %d rows, want %d", len(rows), N)
	}
}

func TestSQLiteDigestStoreCrossSessionIndependent(t *testing.T) {
	db := newTestDigestDB(t)
	s := NewSQLiteDigestStore(db)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_ = s.Save(ctx, sampleDigest("digest-cp-a"+idFor(i), "sA", 0))
	}
	for i := 0; i < 3; i++ {
		_ = s.Save(ctx, sampleDigest("digest-cp-b"+idFor(i), "sB", 0))
	}

	rowsA, _ := s.List(ctx, "sA")
	rowsB, _ := s.List(ctx, "sB")
	if len(rowsA) != 5 || len(rowsB) != 3 {
		t.Fatalf("cross-session: A=%d B=%d, want 5/3", len(rowsA), len(rowsB))
	}
	if rowsA[len(rowsA)-1].Sequence != 5 || rowsB[len(rowsB)-1].Sequence != 3 {
		t.Fatalf("sequence continuation broken: A=%d B=%d", rowsA[len(rowsA)-1].Sequence, rowsB[len(rowsB)-1].Sequence)
	}
}

func TestSQLiteDigestStoreValidationErrors(t *testing.T) {
	db := newTestDigestDB(t)
	s := NewSQLiteDigestStore(db)
	ctx := context.Background()

	if err := s.Save(ctx, nil); err == nil {
		t.Fatalf("Save(nil) = nil, want error")
	}
	if err := s.Save(ctx, &SessionDigest{ID: "x"}); err == nil {
		t.Fatalf("Save(no session) = nil, want error")
	}
	if err := s.Save(ctx, &SessionDigest{SessionID: "s"}); err == nil {
		t.Fatalf("Save(no id) = nil, want error")
	}
	if _, err := s.Latest(ctx, ""); err == nil {
		t.Fatalf("Latest(\"\") = nil, want error")
	}
	if _, err := s.List(ctx, ""); err == nil {
		t.Fatalf("List(\"\") = nil, want error")
	}
	if err := s.DeleteBySession(ctx, ""); err == nil {
		t.Fatalf("DeleteBySession(\"\") = nil, want error")
	}
}

func TestMemoryDigestStoreRoundtrip(t *testing.T) {
	s := NewMemoryDigestStore()
	ctx := context.Background()
	d := sampleDigest("digest-cp-1", "s1", 0)
	if err := s.Save(ctx, d); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if d.Sequence != 1 {
		t.Fatalf("Sequence = %d, want 1", d.Sequence)
	}
	rows, err := s.List(ctx, "s1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "digest-cp-1" {
		t.Fatalf("rows = %+v", rows)
	}
	latest, _ := s.Latest(ctx, "s1")
	if latest == nil || latest.Sequence != 1 {
		t.Fatalf("latest = %+v", latest)
	}
	if err := s.DeleteBySession(ctx, "s1"); err != nil {
		t.Fatalf("DeleteBySession: %v", err)
	}
	rows, _ = s.List(ctx, "s1")
	if len(rows) != 0 {
		t.Fatalf("after delete: %d rows", len(rows))
	}
}

func TestMemoryDigestStoreDuplicateSequence(t *testing.T) {
	s := NewMemoryDigestStore()
	ctx := context.Background()
	d1 := sampleDigest("digest-cp-1", "s1", 1)
	if err := s.Save(ctx, d1); err != nil {
		t.Fatalf("Save #1: %v", err)
	}
	dup := sampleDigest("digest-cp-2", "s1", 1)
	if err := s.Save(ctx, dup); !errors.Is(err, ErrDigestSequenceConflict) {
		t.Fatalf("Save duplicate = %v, want ErrDigestSequenceConflict", err)
	}
}

func idFor(i int) string {
	const digits = "0123456789abcdef"
	if i == 0 {
		return "0"
	}
	out := ""
	for i > 0 {
		out = string(digits[i&0xf]) + out
		i >>= 4
	}
	return out
}