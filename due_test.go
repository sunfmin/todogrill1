package main

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestParseDue(t *testing.T) {
	now := time.Date(2026, 6, 29, 14, 30, 0, 0, time.UTC)
	tests := []struct {
		name    string
		give    string
		want    time.Time
		wantErr bool
	}{
		{name: "iso date", give: "2026-07-15", want: time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)},
		{name: "today", give: "today", want: time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC)},
		{name: "tomorrow", give: "tomorrow", want: time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)},
		{name: "case insensitive", give: "Today", want: time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC)},
		{name: "surrounding spaces", give: "  tomorrow  ", want: time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)},
		{name: "empty", give: "", wantErr: true},
		{name: "word", give: "someday", wantErr: true},
		{name: "wrong format", give: "07/15/2026", wantErr: true},
		{name: "impossible date", give: "2026-13-40", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDue(tt.give, now)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseDue(%q) = %v, want error", tt.give, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDue(%q) unexpected error: %v", tt.give, err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("parseDue(%q) mismatch (-want +got):\n%s", tt.give, diff)
			}
		})
	}
}
