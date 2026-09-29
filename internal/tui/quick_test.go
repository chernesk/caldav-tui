package tui

import "testing"

func TestQuickParse(t *testing.T) {
	// "mon" = Monday 2026-09-28 (see parse_test.go)
	cases := []struct {
		in    string
		title string
		due   string // FormatWhen
		start string
		prio  int
		rrule string
		notes string
	}{
		{in: "buy milk", title: "buy milk"},
		{in: "buy milk tomorrow", title: "buy milk", due: "2026-09-29"},
		{in: "buy milk tomorrow 2pm", title: "buy milk", due: "2026-09-29 14:00"},
		{in: "call mom fri 5pm !high", title: "call mom", due: "2026-10-02 17:00", prio: 1},
		{in: "!low sort photos", title: "sort photos", prio: 9},
		{in: "pay rent by oct 1", title: "pay rent", due: "2026-10-01"},
		{in: "pay rent on friday", title: "pay rent", due: "2026-10-02"},
		{in: "dentist at 3pm", title: "dentist", due: "2026-09-28 15:00"},
		{in: "file taxes in 2 weeks", title: "file taxes", due: "2026-10-12"},
		{in: "review PR due:tomorrow", title: "review PR", due: "2026-09-29"},
		{in: "review PR due:fri 9am", title: "review PR", due: "2026-10-02 09:00"},
		{in: "plan trip start:oct 5 due:oct 20", title: "plan trip", due: "2026-10-20", start: "2026-10-05"},
		{in: "water plants every 3 days", title: "water plants", rrule: "FREQ=DAILY;INTERVAL=3", due: "2026-09-28"},
		{in: "trash every week", title: "trash", rrule: "FREQ=WEEKLY", due: "2026-09-28"},
		{in: "team sync every other week", title: "team sync", rrule: "FREQ=WEEKLY;INTERVAL=2", due: "2026-09-28"},
		{in: "gym every mon,wed,fri", title: "gym", rrule: "FREQ=WEEKLY;BYDAY=MO,WE,FR", due: "2026-09-28"},
		{in: "yoga every tue", title: "yoga", rrule: "FREQ=WEEKLY;BYDAY=TU", due: "2026-09-29"},
		{in: "standup weekdays 9:30am", title: "standup", rrule: "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR", due: "2026-09-28 09:30"},
		{in: "call mom every friday 5pm", title: "call mom", rrule: "FREQ=WEEKLY;BYDAY=FR", due: "2026-10-02 17:00"},
		{in: "vitamins daily", title: "vitamins", rrule: "FREQ=DAILY", due: "2026-09-28"},
		{in: "review budget monthly !med", title: "review budget", rrule: "FREQ=MONTHLY", due: "2026-09-28", prio: 5},
		{in: "email Bob tomorrow // about the Q3 numbers", title: "email Bob", due: "2026-09-29", notes: "about the Q3 numbers"},
		{in: `\!important note tomorrow`, title: "!important note", due: "2026-09-29"},
		{in: `finish \tomorrow`, title: "finish tomorrow"},
		{in: "!! urgent thing", title: "urgent thing", prio: 1},
		{in: "!3 something", title: "something", prio: 3},
		// nothing date-like: the whole thing stays the title
		{in: "Read chapter 5", title: "Read chapter 5"},
		{in: "fix the 5 bugs", title: "fix the 5 bugs"},
		{in: "  spaced   out  ", title: "spaced out"},
		// a lone date-ish word is still the title (needs at least one title word)
		{in: "friday", title: "friday"},
	}
	for _, c := range cases {
		q, err := QuickParse(c.in, mon)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if q.Title != c.title {
			t.Errorf("%q: title %q, want %q", c.in, q.Title, c.title)
		}
		if got := FormatWhen(q.Due); got != c.due {
			t.Errorf("%q: due %q, want %q", c.in, got, c.due)
		}
		if got := FormatWhen(q.Start); got != c.start {
			t.Errorf("%q: start %q, want %q", c.in, got, c.start)
		}
		if q.Priority != c.prio || q.RRule != c.rrule || q.Notes != c.notes {
			t.Errorf("%q: prio=%d rrule=%q notes=%q; want %d %q %q", c.in, q.Priority, q.RRule, q.Notes, c.prio, c.rrule, c.notes)
		}
	}
}

func TestQuickParseErrors(t *testing.T) {
	for _, in := range []string{"", "   ", "!high", "tomorrow !high"[:0], "due:blorp thing", "x start:nope"} {
		if q, err := QuickParse(in, mon); err == nil {
			t.Errorf("%q should fail, got %+v", in, q)
		}
	}
}

func TestQuickEdit(t *testing.T) {
	q, _ := QuickParse("call mom fri 5pm !high every week // hi", mon)
	e := q.Edit()
	if e.Summary == nil || *e.Summary != "call mom" || e.Priority == nil || *e.Priority != 1 ||
		e.RRule == nil || e.Due == nil || !e.Due.HasTime || e.Notes == nil || e.Start != nil {
		t.Errorf("bad edit: %+v", e)
	}
}
