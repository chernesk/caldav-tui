package tui

import (
	"testing"
	"time"
)

// Monday 2026-09-28
var mon = time.Date(2026, 9, 28, 15, 0, 0, 0, time.Local)

func TestParseWhen(t *testing.T) {
	cases := []struct {
		in   string
		want string // FormatWhen output
	}{
		{"today", "2026-09-28"},
		{"tomorrow", "2026-09-29"},
		{"tom 2pm", "2026-09-29 14:00"},
		{"fri", "2026-10-02"},
		{"mon", "2026-10-05"}, // Monday says "next Monday"
		{"sun", "2026-10-04"},
		{"+3d", "2026-10-01"},
		{"in 2 weeks", "2026-10-12"},
		{"1m", "2026-10-28"},
		{"oct 5", "2026-10-05"},
		{"5 oct", "2026-10-05"},
		{"sept 1", "2027-09-01"}, // already past this year -> next year
		{"oct 5 2027", "2027-10-05"},
		{"10/5", "2026-10-05"},
		{"10/5/27", "2027-10-05"},
		{"2026-12-25", "2026-12-25"},
		{"2026-12-25 14:30", "2026-12-25 14:30"},
		{"friday at 9am", "2026-10-02 09:00"},
		{"fri 9:15 pm", "2026-10-02 21:15"},
		{"12am", "2026-09-28 00:00"},
		{"12pm", "2026-09-28 12:00"},
		{"noon", "2026-09-28 12:00"},
		{"17:45", "2026-09-28 17:45"},
		{"", ""},
	}
	for _, c := range cases {
		w, err := ParseWhen(c.in, mon)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if got := FormatWhen(w); got != c.want {
			t.Errorf("%q = %q, want %q", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"blorp", "feb 30", "13/40", "tomorrow friday", "25:00", "2026-02-31", "5"} {
		if w, err := ParseWhen(bad, mon); err == nil {
			t.Errorf("%q should fail, got %v", bad, FormatWhen(w))
		}
	}
}

func TestParsePriority(t *testing.T) {
	for in, want := range map[string]int{"": 0, "none": 0, "high": 1, "H": 1, "med": 5, "low": 9, "3": 3} {
		if got, err := ParsePriority(in); err != nil || got != want {
			t.Errorf("%q = %d,%v want %d", in, got, err, want)
		}
	}
	if _, err := ParsePriority("urgent"); err == nil {
		t.Error("urgent should fail")
	}
}

func TestRepeatRoundTrip(t *testing.T) {
	cases := map[string]string{
		"daily":                      "FREQ=DAILY",
		"weekly":                     "FREQ=WEEKLY",
		"every 2 weeks":              "FREQ=WEEKLY;INTERVAL=2",
		"2 days":                     "FREQ=DAILY;INTERVAL=2",
		"monthly":                    "FREQ=MONTHLY",
		"yearly":                     "FREQ=YEARLY",
		"weekdays":                   "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR",
		"weekly mon,wed":             "FREQ=WEEKLY;BYDAY=MO,WE",
		"every sun":                  "FREQ=WEEKLY;BYDAY=SU",
		"every mon, thu":             "FREQ=WEEKLY;BYDAY=MO,TH",
		"FREQ=MONTHLY;BYMONTHDAY=15": "FREQ=MONTHLY;BYMONTHDAY=15",
		"":                           "",
	}
	for in, want := range cases {
		got, err := ParseRepeat(in)
		if err != nil || got != want {
			t.Errorf("ParseRepeat(%q) = %q,%v want %q", in, got, err, want)
			continue
		}
		// what we show when editing must parse back to the same rule
		if back, err := ParseRepeat(RepeatText(got)); err != nil || back != got {
			t.Errorf("round trip %q -> %q -> %q (%v)", got, RepeatText(got), back, err)
		}
	}
	for _, bad := range []string{"sometimes", "every 0 days", "weekly funday"} {
		if _, err := ParseRepeat(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
	// exotic rules stay raw so editing other fields can't corrupt them
	if got := RepeatText("FREQ=MONTHLY;COUNT=5"); got != "FREQ=MONTHLY;COUNT=5" {
		t.Errorf("exotic: %q", got)
	}
}

func TestPriorityAbbreviations(t *testing.T) {
	for in, want := range map[string]int{"hi": 1, "mid": 5, "lo": 9} {
		if got, err := ParsePriority(in); err != nil || got != want {
			t.Errorf("%q = %d,%v", in, got, err)
		}
	}
}
