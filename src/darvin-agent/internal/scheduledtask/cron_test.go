// Tests for the cron parser and next-fire calculator.

package scheduledtask

import (
	"testing"
	"time"
)

func TestParseCronExpr_AllStars(t *testing.T) {
	expr, err := ParseCronExpr("* * * * *")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(expr.minute.values) != 0 {
		t.Errorf("minute should be * (nil values), got %v", expr.minute.values)
	}
}

func TestParseCronExpr_SpecificValues(t *testing.T) {
	expr, err := ParseCronExpr("0 9 * * 1-5")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wantMin := []int{0}
	if !equalInts(expr.minute.values, wantMin) {
		t.Errorf("minute: got %v, want %v", expr.minute.values, wantMin)
	}
	wantHour := []int{9}
	if !equalInts(expr.hour.values, wantHour) {
		t.Errorf("hour: got %v, want %v", expr.hour.values, wantHour)
	}
	wantDow := []int{1, 2, 3, 4, 5}
	if !equalInts(expr.dow.values, wantDow) {
		t.Errorf("dow: got %v, want %v", expr.dow.values, wantDow)
	}
}

func TestParseCronExpr_SundaySeven(t *testing.T) {
	expr, err := ParseCronExpr("0 9 * * 7")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []int{0} // 7 normalized to 0
	if !equalInts(expr.dow.values, want) {
		t.Errorf("dow: got %v, want %v (7 should normalize to 0)", expr.dow.values, want)
	}
}

func TestParseCronExpr_Step(t *testing.T) {
	expr, err := ParseCronExpr("*/15 * * * *")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []int{0, 15, 30, 45}
	if !equalInts(expr.minute.values, want) {
		t.Errorf("minute */15: got %v, want %v", expr.minute.values, want)
	}
}

func TestParseCronExpr_List(t *testing.T) {
	expr, err := ParseCronExpr("0,30 9,17 * * *")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wantMin := []int{0, 30}
	wantHour := []int{9, 17}
	if !equalInts(expr.minute.values, wantMin) {
		t.Errorf("minute: got %v, want %v", expr.minute.values, wantMin)
	}
	if !equalInts(expr.hour.values, wantHour) {
		t.Errorf("hour: got %v, want %v", expr.hour.values, wantHour)
	}
}

func TestParseCronExpr_NamedMonth(t *testing.T) {
	_, err := ParseCronExpr("0 9 1 jan *")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
}

func TestParseCronExpr_InvalidFields(t *testing.T) {
	cases := []string{
		"* * * *",      // 4 fields
		"60 9 * * *",   // minute out of range
		"0 24 * * *",   // hour out of range
		"0 9 32 * *",   // dom out of range
		"0 9 * 13 *",   // month out of range
		"0 9 * 0 *",    // month out of range (min 1)
	}
	for _, expr := range cases {
		if _, err := ParseCronExpr(expr); err == nil {
			t.Errorf("%q should fail to parse", expr)
		}
	}
}

func TestNext_DailyNineAM(t *testing.T) {
	expr, _ := ParseCronExpr("0 9 * * *")
	from := time.Date(2026, 1, 1, 8, 30, 0, 0, time.UTC)
	got, err := expr.Next(from, time.UTC)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	want := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNext_WeekdayNineAM(t *testing.T) {
	expr, _ := ParseCronExpr("0 9 * * 1-5")
	// 2026-01-03 is Saturday; next is Monday 2026-01-05 09:00.
	from := time.Date(2026, 1, 3, 12, 0, 0, 0, time.UTC)
	got, err := expr.Next(from, time.UTC)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	want := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNext_FifteenMinutes(t *testing.T) {
	expr, _ := ParseCronExpr("*/15 * * * *")
	from := time.Date(2026, 1, 1, 10, 7, 0, 0, time.UTC)
	got, err := expr.Next(from, time.UTC)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	want := time.Date(2026, 1, 1, 10, 15, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}