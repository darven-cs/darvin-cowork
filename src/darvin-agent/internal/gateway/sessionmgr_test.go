package gateway

import (
	"regexp"
	"testing"
)

var idRe = regexp.MustCompile(`^[A-Za-z0-9]{21}$`)

// TestDefaultSessionRegisteredUpFront guards the subscribe-before-prompt
// path: agent.subscribe_events rejects ids that Has() doesn't know, so the
// default session must exist from construction or a client would have to
// race the prompt reply and could miss the run's opening events.
func TestDefaultSessionRegisteredUpFront(t *testing.T) {
	m := NewSessionManager()
	if !m.Has(DefaultSessionID) {
		t.Fatalf("expected Has(%q) true before any CreateOrGet", DefaultSessionID)
	}
}

// TestCreateOrGetDefaultsToDefaultSession pins the single-session
// contract: an empty id resolves to DefaultSessionID rather than minting a
// fresh one, because that is the id the Agent stamps on its events and
// therefore the only id a WS subscriber can usefully key on.
func TestCreateOrGetDefaultsToDefaultSession(t *testing.T) {
	m := NewSessionManager()
	sess, msgID, err := m.CreateOrGet("")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if sess.ID != DefaultSessionID {
		t.Fatalf("session id = %q, want %q", sess.ID, DefaultSessionID)
	}
	if !idRe.MatchString(msgID) {
		t.Fatalf("message id shape: %q", msgID)
	}
	if !m.Has(sess.ID) {
		t.Fatalf("expected Has(%s) true", sess.ID)
	}
}

// TestCreateOrGetEmptyAndDefaultAgree covers the two spellings the handler
// accepts — omitted sessionId and an explicit "default" — resolving to the
// same session instance.
func TestCreateOrGetEmptyAndDefaultAgree(t *testing.T) {
	m := NewSessionManager()
	a, _, _ := m.CreateOrGet("")
	b, _, _ := m.CreateOrGet(DefaultSessionID)
	if a != b {
		t.Fatalf("expected same session instance, got %p vs %p", a, b)
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
// over 10k draws is well under 1e-15. Session ids are fixed in the
// single-session model, so the property is asserted on message ids.
func TestNanoidUniqueness(t *testing.T) {
	m := NewSessionManager()
	seen := make(map[string]struct{}, 10000)
	for i := 0; i < 10000; i++ {
		_, msgID, _ := m.CreateOrGet("")
		if !idRe.MatchString(msgID) {
			t.Fatalf("message id shape at %d: %q", i, msgID)
		}
		if _, dup := seen[msgID]; dup {
			t.Fatalf("collision at %d: %q", i, msgID)
		}
		seen[msgID] = struct{}{}
	}
}
