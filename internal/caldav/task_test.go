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
