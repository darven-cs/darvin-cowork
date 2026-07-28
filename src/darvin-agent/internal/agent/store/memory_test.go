package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"darvin-cowork/backend/internal/agent/llm"
	"darvin-cowork/backend/internal/agent/session"
)

func TestMemoryStoreSaveLoad(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore()

	src := session.NewSession("s1")
	src.Append(llm.Message{Role: llm.RoleUser, Content: "hello"})
	src.Append(llm.Message{Role: llm.RoleAssistant, Content: "world"})
	if err := ms.Save(ctx, src); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := ms.Load(ctx, "s1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Len() != 2 {
		t.Fatalf("Len = %d, want 2", got.Len())
	}
	if got.Messages()[1].Content != "world" {
		t.Errorf("last content = %q, want world", got.Messages()[1].Content)
	}
}

func TestMemoryStoreLoadNotFound(t *testing.T) {
	ms := NewMemoryStore()
	_, err := ms.Load(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestMemoryStoreDelete(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore()
	s := session.NewSession("s1")
	if err := ms.Save(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := ms.Delete(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := ms.Load(ctx, "s1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("after Delete Load err = %v, want ErrNotFound", err)
	}
}

func TestMemoryStoreList(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore()
	for _, id := range []string{"a", "b", "c"} {
		if err := ms.Save(ctx, session.NewSession(id)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond) // ensure distinct UpdatedAt
	}
	list, err := ms.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("len(list) = %d, want 3", len(list))
	}
	if list[0].ID != "c" {
		t.Errorf("first in list = %q, want c (newest)", list[0].ID)
	}
}

func TestMemoryStoreDeepCopyOnSave(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore()
	src := session.NewSession("s1")
	src.Append(llm.Message{Role: llm.RoleUser, Content: "v1"})
	if err := ms.Save(ctx, src); err != nil {
		t.Fatal(err)
	}
	// mutate source after save
	src.Append(llm.Message{Role: llm.RoleAssistant, Content: "v2"})

	got, err := ms.Load(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Len() != 1 {
		t.Errorf("stored Len = %d, want 1 (mutation leaked into store)", got.Len())
	}
}
