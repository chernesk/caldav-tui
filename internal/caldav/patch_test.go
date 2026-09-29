package caldav

import (
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

func sp(s string) *string { return &s }
func ip(i int) *int       { return &i }

func lines(raw string) map[string]string {
	m := map[string]string{}
	for _, l := range unfold(raw) {
		n, _, _ := splitProp(l)
		if _, seen := m[n]; !seen {
			m[n] = l
		}
	}
	return m
}

func wrap(body string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VTODO\r\nUID:u1\r\n" + body + "END:VTODO\r\nEND:VCALENDAR\r\n"
}

func TestApplyEditPreservesUnmanagedAndAlarms(t *testing.T) {
	raw := wrap("SUMMARY:Old\r\nX-APPLE-SORT-ORDER:42\r\nCATEGORIES:home\r\n" +
		"BEGIN:VALARM\r\nACTION:DISPLAY\r\nDESCRIPTION:alarm text\r\nTRIGGER:-PT10M\r\nEND:VALARM\r\n" +
		"DESCRIPTION:real notes\r\n")
	out, err := ApplyEdit(raw, Edit{Summary: sp("New, title"), Notes: sp("")}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"SUMMARY:New\\, title", "X-APPLE-SORT-ORDER:42", "CATEGORIES:home",
		"BEGIN:VALARM", "DESCRIPTION:alarm text", "TRIGGER:-PT10M"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "real notes") {
		t.Errorf("notes not cleared:\n%s", out)
	}
	if !strings.Contains(out, "LAST-MODIFIED:20260928T120000Z") {
		t.Errorf("no LAST-MODIFIED:\n%s", out)
	}
	if !strings.HasSuffix(out, "END:VCALENDAR\r\n") {
		t.Errorf("bad tail: %q", out)
	}
}

func TestNewPropsInsertedBeforeAlarm(t *testing.T) {
	raw := wrap("SUMMARY:x\r\nBEGIN:VALARM\r\nTRIGGER:-PT5M\r\nEND:VALARM\r\n")
	out, _ := ApplyEdit(raw, Edit{Priority: ip(1)}, now)
	if strings.Index(out, "PRIORITY:1") > strings.Index(out, "BEGIN:VALARM") {
		t.Errorf("PRIORITY after VALARM:\n%s", out)
	}
}

func TestDueStartTypeMatch(t *testing.T) {
	raw := wrap("SUMMARY:x\r\nDTSTART;VALUE=DATE:20260901\r\n")
	due := When{T: time.Date(2026, 9, 30, 14, 30, 0, 0, time.UTC), HasTime: true}
	out, _ := ApplyEdit(raw, Edit{Due: &due}, now)
	l := lines(out)
	if l["DUE"] != "DUE:20260930T143000" || l["DTSTART"] != "DTSTART:20260901T000000" {
		t.Errorf("got DUE=%q DTSTART=%q", l["DUE"], l["DTSTART"])
	}
	// and back to all-day
	d2 := When{T: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	out, _ = ApplyEdit(out, Edit{Due: &d2}, now)
	l = lines(out)
	if l["DUE"] != "DUE;VALUE=DATE:20260930" || l["DTSTART"] != "DTSTART;VALUE=DATE:20260901" {
		t.Errorf("got DUE=%q DTSTART=%q", l["DUE"], l["DTSTART"])
	}
}

func TestUnchangedDueKeepsTZID(t *testing.T) {
	raw := wrap("SUMMARY:x\r\nDUE;TZID=America/Los_Angeles:20260930T140000\r\n")
	out, _ := ApplyEdit(raw, Edit{Summary: sp("y")}, now)
	if !strings.Contains(out, "DUE;TZID=America/Los_Angeles:20260930T140000") {
		t.Errorf("DUE rewritten:\n%s", out)
	}
}

func TestFoldLongUTF8(t *testing.T) {
	long := strings.Repeat("é", 100)
	out, _ := ApplyEdit(wrap("SUMMARY:x\r\n"), Edit{Summary: &long}, now)
	for _, l := range strings.Split(out, "\r\n") {
		if len(l) > 75 {
			t.Errorf("line over 75 octets (%d): %q", len(l), l)
		}
	}
	if got := lines(out)["SUMMARY"]; got != "SUMMARY:"+long {
		t.Errorf("unfold mismatch: %q", got)
	}
}

func TestNewRaw(t *testing.T) {
	due := When{T: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)}
	out, err := NewRaw("abc", "parent1", Edit{Summary: sp("Hello"), Due: &due, Priority: ip(5), RRule: sp("FREQ=WEEKLY")}, now)
	if err != nil {
		t.Fatal(err)
	}
	l := lines(out)
	for k, v := range map[string]string{"SUMMARY": "SUMMARY:Hello", "UID": "UID:abc", "RELATED-TO": "RELATED-TO:parent1",
		"DUE": "DUE;VALUE=DATE:20261002", "PRIORITY": "PRIORITY:5", "RRULE": "RRULE:FREQ=WEEKLY", "STATUS": "STATUS:NEEDS-ACTION"} {
		if l[k] != v {
			t.Errorf("%s = %q, want %q", k, l[k], v)
		}
	}
}

func TestSetDoneRoundTrip(t *testing.T) {
	out, _ := SetDone(wrap("SUMMARY:x\r\nSTATUS:NEEDS-ACTION\r\n"), true, now)
	l := lines(out)
	if l["STATUS"] != "STATUS:COMPLETED" || l["COMPLETED"] != "COMPLETED:20260928T120000Z" {
		t.Errorf("complete: %v", l)
	}
	out, _ = SetDone(out, false, now)
	l = lines(out)
	if l["STATUS"] != "STATUS:NEEDS-ACTION" || l["COMPLETED"] != "" || l["PERCENT-COMPLETE"] != "" {
		t.Errorf("reopen: %v", l)
	}
}

func TestCompleteNonRecurring(t *testing.T) {
	out, res, err := Complete(wrap("SUMMARY:x\r\nDUE;VALUE=DATE:20260930\r\n"), now)
	if err != nil || res != Completed || lines(out)["STATUS"] != "STATUS:COMPLETED" {
		t.Errorf("res=%v err=%v\n%s", res, err, out)
	}
}

func TestCompleteRecurringAdvances(t *testing.T) {
	cases := []struct {
		name, body, wantDue, wantStart string
	}{
		{"daily all-day", "DUE;VALUE=DATE:20260930\r\nDTSTART;VALUE=DATE:20260928\r\nRRULE:FREQ=DAILY;INTERVAL=2\r\n",
			"DUE;VALUE=DATE:20261002", "DTSTART;VALUE=DATE:20260930"},
		// 2026-09-30 is a Wednesday; next of MO,WE,FR is Friday
		{"weekly byday", "DUE;VALUE=DATE:20260930\r\nRRULE:FREQ=WEEKLY;BYDAY=MO,WE,FR\r\n", "DUE;VALUE=DATE:20261002", ""},
		// Friday -> next Monday
		{"weekly wrap", "DUE;VALUE=DATE:20261002\r\nRRULE:FREQ=WEEKLY;BYDAY=MO,WE,FR\r\n", "DUE;VALUE=DATE:20261005", ""},
		{"monthly timed tzid", "DUE;TZID=America/Los_Angeles:20260915T093000\r\nRRULE:FREQ=MONTHLY\r\n",
			"DUE;TZID=America/Los_Angeles:20261015T093000", ""},
		{"yearly utc", "DUE:20260928T170000Z\r\nRRULE:FREQ=YEARLY\r\n", "DUE:20270928T170000Z", ""},
		{"only dtstart", "DTSTART;VALUE=DATE:20260928\r\nRRULE:FREQ=WEEKLY\r\n", "", "DTSTART;VALUE=DATE:20261005"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, res, err := Complete(wrap("SUMMARY:r\r\nSTATUS:NEEDS-ACTION\r\n"+c.body), now)
			if err != nil || res != Advanced {
				t.Fatalf("res=%v err=%v\n%s", res, err, out)
			}
			l := lines(out)
			if c.wantDue != "" && l["DUE"] != c.wantDue {
				t.Errorf("DUE=%q want %q", l["DUE"], c.wantDue)
			}
			if c.wantStart != "" && l["DTSTART"] != c.wantStart {
				t.Errorf("DTSTART=%q want %q", l["DTSTART"], c.wantStart)
			}
			if l["STATUS"] != "STATUS:NEEDS-ACTION" || l["COMPLETED"] != "" {
				t.Errorf("should stay open: %v", l)
			}
		})
	}
}

func TestCompleteRecurringExhausted(t *testing.T) {
	// UNTIL before next occurrence
	out, res, _ := Complete(wrap("SUMMARY:r\r\nDUE;VALUE=DATE:20260930\r\nRRULE:FREQ=DAILY;UNTIL=20260930\r\n"), now)
	if res != Completed || lines(out)["STATUS"] != "STATUS:COMPLETED" {
		t.Errorf("UNTIL: res=%v\n%s", res, out)
	}
	// COUNT=1 is the last one; COUNT=3 decrements
	_, res, _ = Complete(wrap("SUMMARY:r\r\nDUE;VALUE=DATE:20260930\r\nRRULE:FREQ=DAILY;COUNT=1\r\n"), now)
	if res != Completed {
		t.Errorf("COUNT=1: res=%v", res)
	}
	out, res, _ = Complete(wrap("SUMMARY:r\r\nDUE;VALUE=DATE:20260930\r\nRRULE:FREQ=DAILY;COUNT=3\r\n"), now)
	if res != Advanced || lines(out)["RRULE"] != "RRULE:FREQ=DAILY;COUNT=2" {
		t.Errorf("COUNT=3: res=%v rrule=%q", res, lines(out)["RRULE"])
	}
}

func TestCompleteBadRRuleFallsBackToComplete(t *testing.T) {
	_, res, err := Complete(wrap("SUMMARY:r\r\nDUE;VALUE=DATE:20260930\r\nRRULE:FREQ=BOGUS\r\n"), now)
	if err != nil || res != Completed {
		t.Errorf("res=%v err=%v", res, err)
	}
}

func TestNoVTODO(t *testing.T) {
	if _, err := ApplyEdit("BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n", Edit{}, now); err == nil {
		t.Error("expected error")
	}
}
