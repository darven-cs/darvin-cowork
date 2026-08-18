// Tests for the engine helper functions: backoff ladder, computeNextFire
// (at / every / cron), and validateScheduleBody.

package scheduledtask

import (
	"testing"
	"time"

	"darvin-cowork/backend/internal/agents/store"
)

func TestBackoffFor_Ladder(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 30 * time.Second},
		{2, 30 * time.Second},
		{3, 1 * time.Minute},
		{4, 5 * time.Minute},
		{5, 15 * time.Minute},
		{6, 60 * time.Minute},
		{10, 60 * time.Minute},
	}
	for _, c := range cases {
		if got := backoffFor(c.attempt); got != c.want {
			t.Errorf("backoffFor(%d) = %v, want %v", c.attempt, got, c.want)
		}
	}
}

func TestComputeNextFire_At(t *testing.T) {
	body := store.ScheduleBody{Kind: "at", At: "2027-01-15T09:00:00Z"}
	got, err := computeNextFire(body, time.Now())
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	want := time.Date(2027, 1, 15, 9, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestComputeNextFire_At_Invalid(t *testing.T) {
	body := store.ScheduleBody{Kind: "at", At: "not-a-time"}
	if _, err := computeNextFire(body, time.Now()); err == nil {
		t.Error("expected error for invalid RFC3339")
	}
}

func TestComputeNextFire_Every(t *testing.T) {
	now := time.UnixMilli(1700000000000)
	body := store.ScheduleBody{Kind: "every", EveryMs: 60000} // every minute
	got, err := computeNextFire(body, now)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	// next must be in the future, on a minute boundary, aligned with anchor.
	if !got.After(now) {
		t.Errorf("got %v should be after now %v", got, now)
	}
	if got.UnixMilli()%60000 != 0 {
		t.Errorf("got %v not aligned to 60s boundary", got)
	}
}

func TestComputeNextFire_Every_ZeroInterval(t *testing.T) {
	body := store.ScheduleBody{Kind: "every", EveryMs: 0}
	if _, err := computeNextFire(body, time.Now()); err == nil {
		t.Error("expected error for everyMs <= 0")
	}
}

func TestComputeNextFire_Cron(t *testing.T) {
	body := store.ScheduleBody{Kind: "cron", Expr: "0 9 * * *", TZ: "UTC"}
	from := time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)
	got, err := computeNextFire(body, from)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	want := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestComputeNextFire_Unknown(t *testing.T) {
	body := store.ScheduleBody{Kind: "weekly"}
	if _, err := computeNextFire(body, time.Now()); err == nil {
		t.Error("expected error for unknown kind")
	}
}

func TestValidateScheduleBody(t *testing.T) {
	cases := []struct {
		name string
		body store.ScheduleBody
		ok   bool
	}{
		{"at ok", store.ScheduleBody{Kind: "at", At: "2027-01-15T09:00:00Z"}, true},
		{"at missing", store.ScheduleBody{Kind: "at"}, false},
		{"at invalid", store.ScheduleBody{Kind: "at", At: "bogus"}, false},
		{"every ok", store.ScheduleBody{Kind: "every", EveryMs: 60000}, true},
		{"every zero", store.ScheduleBody{Kind: "every", EveryMs: 0}, false},
		{"cron ok", store.ScheduleBody{Kind: "cron", Expr: "0 9 * * *"}, true},
		{"cron missing expr", store.ScheduleBody{Kind: "cron"}, false},
		{"cron bad expr", store.ScheduleBody{Kind: "cron", Expr: "bad"}, false},
		{"unknown", store.ScheduleBody{Kind: "weekly"}, false},
	}
	for _, c := range cases {
		err := validateScheduleBody(c.body)
		got := err == nil
		if got != c.ok {
			t.Errorf("%s: ok=%v err=%v", c.name, got, err)
		}
	}
}