package tui

import (
	"testing"
	"time"

	"github.com/chernesk/caldav-tui/internal/caldav"
)

func TestParseAlarms(t *testing.T) {
	cases := map[string][]caldav.Alarm{
		"at due":                     {{End: true}},
		"at start":                   {{}},
		"10 min before":              {{Offset: -10 * time.Minute, End: true}},
		"10m":                        {{Offset: -10 * time.Minute, End: true}},
		"1 day after due":            {{Offset: 24 * time.Hour, End: true}},
		"2h before start":            {{Offset: -2 * time.Hour}},
		"1 day 2 hours before start": {{Offset: -26 * time.Hour}},
		"at due, 1 day after due":    {{End: true}, {Offset: 24 * time.Hour, End: true}},
		"at due, at due":             {{End: true}},
		"":                           {},
		"none":                       {},
	}
	for in, want := range cases {
		got, err := ParseAlarms(in)
		if err != nil || len(got) != len(want) {
			t.Errorf("%q: %v, %v", in, got, err)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%q[%d] = %v want %v", in, i, got[i], want[i])
			}
		}
		// text round trip
		if back, err := ParseAlarms(AlarmsText(got)); err != nil || AlarmsText(back) != AlarmsText(got) {
			t.Errorf("round trip %q -> %q: %v", in, AlarmsText(got), err)
		}
	}
	for _, bad := range []string{"soon", "before", "10 parsecs before", "at noon"} {
		if _, err := ParseAlarms(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestQuickAlarms(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	q, err := QuickParse("pay rent fri 5pm alarm:10 min before alarm:at due !high", now)
	if err != nil {
		t.Fatal(err)
	}
	if q.Title != "pay rent" || AlarmsText(q.Alarms) != "10 min before due, at due" || q.Due.IsZero() {
		t.Errorf("%+v", q)
	}
	if e := q.Edit(); e.Alarms == nil || len(*e.Alarms) != 2 {
		t.Errorf("edit alarms: %v", e.Alarms)
	}
	// a date after the alarm phrase is still the due date
	q, _ = QuickParse("call mom alarm:1h fri", now)
	if q.Title != "call mom" || AlarmsText(q.Alarms) != "1 hour before due" || q.Due.IsZero() {
		t.Errorf("%+v", q)
	}
}
