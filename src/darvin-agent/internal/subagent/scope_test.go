// Tests for scope resolution.

package subagent

import (
	"reflect"
	"testing"
)

func TestResolveScope_DefaultWhenEmpty(t *testing.T) {
	got := ResolveScope(nil)
	want := defaultScope()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("default scope mismatch: got %v want %v", got, want)
	}
}

func TestResolveScope_DropsForbidden(t *testing.T) {
	got := ResolveScope([]string{"read_file", "shell", "delegate_subagent"})
	want := []string{"read_file"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("forbidden drop mismatch: got %v want %v", got, want)
	}
}

func TestResolveScope_DropsDuplicates(t *testing.T) {
	got := ResolveScope([]string{"read_file", "read_file", "grep"})
	want := []string{"read_file", "grep"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dedupe mismatch: got %v want %v", got, want)
	}
}

func TestResolveScope_FallsBackWhenAllForbidden(t *testing.T) {
	got := ResolveScope([]string{"shell", "delegate_subagent"})
	want := defaultScope()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("all-forbidden fallback mismatch: got %v want %v", got, want)
	}
}
