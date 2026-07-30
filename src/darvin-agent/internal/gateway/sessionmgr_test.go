package gateway

import (
	"regexp"
	"testing"
)

var idRe = regexp.MustCompile(`^[A-Za-z0-9]{21}$`)

func TestCreateOrGetNewSession(t *testing.T) {
	m := NewSessionManager()
	sess, msgID, err := m.CreateOrGet("")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !idRe.MatchString(sess.ID) {
		t.Fatalf("session id shape: %q", sess.ID)
	}
	if !idRe.MatchString(msgID) {
		t.Fatalf("message id shape: %q", msgID)
	}
	if !m.Has(sess.ID) {
		t.Fatalf("expected Has(%s) true", sess.ID)
	}
}

func TestCreateOrGetReusesSession(t *testing.T) {
	m := NewSessionManager()
	a, _, _ := m.CreateOrGet("")
	b, msgID2, _ := m.CreateOrGet(a.ID)
	if a != b {
		t.Fatalf("expected same session instance, got %p vs %p", a, b)
	}
	if !idRe.MatchString(msgID2) {
		t.Fatalf("msgID shape: %q", msgID2)
	}
}

func TestCreateOrGetDistinctMessageIDs(t *testing.T) {
	m := NewSessionManager()
	_, a, _ := m.CreateOrGet("")
	_, b, _ := m.CreateOrGet("")
	if a == b {
		t.Fatalf("expected distinct message ids: %q", a)
	}
}

func TestHasReturnsFalseForUnknown(t *testing.T) {
	m := NewSessionManager()
	if m.Has("nope") {
		t.Fatalf("expected Has false for unknown id")
	}
}

// TestNanoidUniqueness is the spec §7 property test: 10000 ids, no
// repeats. The alphabet is 62 chars and length 21 — collision chance
// over 10k draws is well under 1e-15.
func TestNanoidUniqueness(t *testing.T) {
	m := NewSessionManager()
	seen := make(map[string]struct{}, 10000)
	for i := 0; i < 10000; i++ {
		s, _, _ := m.CreateOrGet("")
		if _, dup := seen[s.ID]; dup {
			t.Fatalf("collision at %d: %q", i, s.ID)
		}
		seen[s.ID] = struct{}{}
	}
}
