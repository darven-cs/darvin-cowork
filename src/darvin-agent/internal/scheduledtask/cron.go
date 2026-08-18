// Hand-rolled 5-field cron parser and next-fire calculator. Format is
// "minute hour day-of-month month day-of-week"; day-of-week 0 and 7
// both mean Sunday. IANA timezone names are resolved via
// time.LoadLocation; an unknown tz falls back to system local.

package scheduledtask

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// cronField is one parsed cron field. nil values means "*".
type cronField struct {
	min, max int
	values   []int
}

func (f cronField) match(v int) bool {
	if f.values == nil {
		return v >= f.min && v <= f.max
	}
	for _, x := range f.values {
		if x == v {
			return true
		}
	}
	return false
}

var (
	monthNames = map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}
	dowNames   = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}
)

// parseCronField parses one cron field. Supported syntax:
//   *        any value
//   N        exact match
//   N,M      list
//   N-M      inclusive range
//   */N      every Nth value
//   N-M/S    stepped range
func parseCronField(token string, min, max int, names map[string]int) (cronField, error) {
	f := cronField{min: min, max: max}
	token = strings.TrimSpace(strings.ToLower(token))
	if token == "" {
		return f, fmt.Errorf("empty cron field")
	}
	if token == "*" {
		return f, nil
	}
	parts := strings.Split(token, ",")
	seen := map[int]bool{}
	for _, p := range parts {
		step := 1
		if i := strings.Index(p, "/"); i >= 0 {
			s, err := strconv.Atoi(p[i+1:])
			if err != nil || s <= 0 {
				return f, fmt.Errorf("bad step in %q", p)
			}
			step = s
			p = p[:i]
		}
		var lo, hi int
		switch {
		case p == "*":
			lo, hi = min, max
		case strings.Contains(p, "-"):
			i := strings.Index(p, "-")
			lo = resolveCronToken(p[:i], names)
			hi = resolveCronToken(p[i+1:], names)
		default:
			v := resolveCronToken(p, names)
			lo, hi = v, v
		}
		if lo < min || hi > max || lo > hi {
			return f, fmt.Errorf("out of range %d-%d in %q", lo, hi, p)
		}
		for v := lo; v <= hi; v += step {
			if !seen[v] {
				seen[v] = true
				f.values = append(f.values, v)
			}
		}
	}
	return f, nil
}

func resolveCronToken(tok string, names map[string]int) int {
	if v, ok := names[tok]; ok {
		return v
	}
	v, err := strconv.Atoi(tok)
	if err != nil {
		return -1
	}
	return v
}

// cronExpr is the parsed 5-field schedule.
type cronExpr struct {
	minute, hour, dom, month, dow cronField
}

// ParseCronExpr parses a 5-field cron expression.
func ParseCronExpr(expr string) (cronExpr, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return cronExpr{}, fmt.Errorf("cron needs 5 fields, got %d", len(fields))
	}
	var c cronExpr
	var err error
	if c.minute, err = parseCronField(fields[0], 0, 59, nil); err != nil {
		return cronExpr{}, fmt.Errorf("minute: %w", err)
	}
	if c.hour, err = parseCronField(fields[1], 0, 23, nil); err != nil {
		return cronExpr{}, fmt.Errorf("hour: %w", err)
	}
	if c.dom, err = parseCronField(fields[2], 1, 31, nil); err != nil {
		return cronExpr{}, fmt.Errorf("dom: %w", err)
	}
	if c.month, err = parseCronField(fields[3], 1, 12, monthNames); err != nil {
		return cronExpr{}, fmt.Errorf("month: %w", err)
	}
	if c.dow, err = parseCronField(fields[4], 0, 7, dowNames); err != nil {
		return cronExpr{}, fmt.Errorf("dow: %w", err)
	}
	// Treat 7 as Sunday (cron convention) so `0 9 * * 7` matches Sun.
	if c.dow.values != nil {
		for i, v := range c.dow.values {
			if v == 7 {
				c.dow.values[i] = 0
			}
		}
	}
	return c, nil
}

// Next returns the next time strictly after from that matches expr in loc.
// When both dom and dow are restricted, the day matches if either holds
// (cron convention).
func (c cronExpr) Next(from time.Time, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.Local
	}
	t := from.In(loc).Add(time.Minute).Truncate(time.Minute)
	const limit = 366 * 24 * 60 // ~1 year search cap
	for i := 0; i < limit; i++ {
		if !c.month.match(int(t.Month())) {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc)
			continue
		}
		domMatch := c.dom.match(t.Day())
		dowMatch := c.dow.match(int(t.Weekday()))
		if c.dom.values != nil && c.dow.values != nil {
			if !domMatch && !dowMatch {
				t = t.AddDate(0, 0, 1).Truncate(24 * time.Hour)
				continue
			}
		} else if c.dom.values != nil && !domMatch {
			t = t.AddDate(0, 0, 1).Truncate(24 * time.Hour)
			continue
		} else if c.dow.values != nil && !dowMatch {
			t = t.AddDate(0, 0, 1).Truncate(24 * time.Hour)
			continue
		}
		if !c.hour.match(t.Hour()) {
			t = t.Add(time.Hour).Truncate(time.Hour)
			continue
		}
		if !c.minute.match(t.Minute()) {
			t = t.Add(time.Minute)
			continue
		}
		return t, nil
	}
	return time.Time{}, fmt.Errorf("no match within %d minutes", limit)
}