package main

import (
	"fmt"
	"strings"
	"time"
)

// parseDue parses a user-supplied due date at day granularity. It accepts
// "today", "tomorrow" (relative to now), and an explicit YYYY-MM-DD date.
// now is injected so the relative forms are testable. The returned time is
// midnight in now's location.
func parseDue(s string, now time.Time) (time.Time, error) {
	trimmed := strings.TrimSpace(s)
	switch strings.ToLower(trimmed) {
	case "today":
		return dayOf(now), nil
	case "tomorrow":
		return dayOf(now.AddDate(0, 0, 1)), nil
	}
	d, err := time.ParseInLocation(dayLayout, trimmed, now.Location())
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid due date %q (want YYYY-MM-DD, today, or tomorrow)", s)
	}
	return d, nil
}

// dayOf truncates t to midnight in its own location.
func dayOf(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
