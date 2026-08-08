// Tests for the renderer-facing skill wire projections.

package skills

import (
	"encoding/json"
	"testing"
	"time"
)

// TestToSummaryCarriesUserInvocable pins the userInvocable wire field the
// renderer's slash menu depends on (src/shared/darvin-api.ts). A drift here
// is invisible in Go but silently hides the skill from the autocomplete.
func TestToSummaryCarriesUserInvocable(t *testing.T) {
	e := &SkillEntry{
		ID:            "code-review",
		Name:          "Code Review",
		Enabled:       true,
		UserInvocable: true,
		LoadedAt:      time.Now(),
	}
	ws := ToSummary(e)
	if !ws.UserInvocable {
		t.Fatalf("UserInvocable = false, want true")
	}
	b, err := json.Marshal(ws)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["userInvocable"] != true {
		t.Fatalf("wire userInvocable = %v, want true", m["userInvocable"])
	}
}

func TestToSummaryDefaultsUserInvocableFalse(t *testing.T) {
	e := &SkillEntry{ID: "s", Enabled: true, LoadedAt: time.Now()}
	if ToSummary(e).UserInvocable {
		t.Fatalf("UserInvocable should default false")
	}
}
