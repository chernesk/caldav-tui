package caldav

import (
	"strings"
	"testing"

	"github.com/emersion/go-ical"
)

const sample = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//x//EN\r\nBEGIN:VTODO\r\nUID:child-1\r\nDTSTAMP:20260901T000000Z\r\n" +
	"SUMMARY:Buy milk\\, eggs\r\nDUE;VALUE=DATE:20260930\r\nPRIORITY:1\r\nRRULE:FREQ=WEEKLY;BYDAY=MO\r\n" +
	"RELATED-TO:parent-1\r\nX-CUSTOM:keep me\r\nSTATUS:NEEDS-ACTION\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"

func TestParseTask(t *testing.T) {
	cal, err := ical.NewDecoder(strings.NewReader(sample)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	task, ok := ParseTask("/c/child-1.ics", `"e1"`, "/c/", cal)
	if !ok {
		t.Fatal("no VTODO found")
	}
	if task.Summary != "Buy milk, eggs" || task.UID != "child-1" || task.Priority != 1 {
		t.Errorf("bad basics: %+v", task)
	}
	if !task.DueAllDay || task.Due.Format("2006-01-02") != "2026-09-30" {
		t.Errorf("bad due: %v allDay=%v", task.Due, task.DueAllDay)
	}
	if task.Parent != "parent-1" || task.RRule != "FREQ=WEEKLY;BYDAY=MO" {
		t.Errorf("bad parent/rrule: %+v", task)
	}
	if !strings.Contains(task.Raw, "X-CUSTOM:keep me") {
		t.Errorf("raw lost unknown property:\n%s", task.Raw)
	}
}

func TestNormETag(t *testing.T) {
	for in, want := range map[string]string{
		"abc123":     `"abc123"`, // what go-webdav hands us from a REPORT
		`"abc123"`:   `"abc123"`, // already wire form (PUT/HEAD header)
		`W/"weak"`:   `W/"weak"`,
		"":           "",
		"  spaced  ": `"spaced"`,
	} {
		if got := NormETag(in); got != want {
			t.Errorf("NormETag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseTaskQuotesETag(t *testing.T) {
	cal, _ := ical.NewDecoder(strings.NewReader(sample)).Decode()
	task, _ := ParseTask("/c/x.ics", "5105d98a", "/c/", cal)
	if task.ETag != `"5105d98a"` {
		t.Errorf("cached ETag %q would be sent unquoted in If-Match", task.ETag)
	}
}

// How Tasks.org (via DAVx5) links a subtask, taken from a real task on the account.
func TestParseTasksOrgSubtask(t *testing.T) {
	child := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VTODO\r\nUID:kid\r\nSUMMARY:Blanket\r\nRELATED-TO;RELTYPE=PARENT:mom\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"
	cal, _ := ical.NewDecoder(strings.NewReader(child)).Decode()
	if task, _ := ParseTask("/c/kid.ics", "e", "/c/", cal); task.Parent != "mom" {
		t.Errorf("parent = %q", task.Parent)
	}
	// a link in the other direction (this task lists its CHILD) must not be mistaken for a parent
	mom := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VTODO\r\nUID:mom\r\nSUMMARY:Walmart\r\nRELATED-TO;RELTYPE=CHILD:kid\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"
	cal, _ = ical.NewDecoder(strings.NewReader(mom)).Decode()
	if task, _ := ParseTask("/c/mom.ics", "e", "/c/", cal); task.Parent != "" {
		t.Errorf("CHILD link read as parent: %q", task.Parent)
	}
}
