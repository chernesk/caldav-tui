package caldav

import (
	"bytes"
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

// Task is the subset of a VTODO the UI cares about. Raw keeps the original
// iCalendar text so later writes can patch only the properties we manage.
type Task struct {
	Href        string
	ETag        string
	Calendar    string // calendar path
	UID         string
	Summary     string
	Notes       string
	Status      string
	Priority    int // 0 = none, 1 = highest .. 9 = lowest
	Due         time.Time
	DueAllDay   bool
	Start       time.Time
	StartAllDay bool
	Completed   time.Time
	RRule       string
	Parent      string // UID from RELATED-TO (default/PARENT reltype)
	Raw         string
}

func (t Task) Done() bool { return t.Status == "COMPLETED" || t.Status == "CANCELLED" }

// ParseTask extracts the first VTODO from an iCalendar object.
func ParseTask(href, etag, calendar string, cal *ical.Calendar) (Task, bool) {
	for _, c := range cal.Children {
		if c.Name != ical.CompToDo {
			continue
		}
		t := Task{Href: href, ETag: etag, Calendar: calendar, Status: "NEEDS-ACTION"}
		p := c.Props
		t.UID, _ = p.Text(ical.PropUID)
		t.Summary, _ = p.Text(ical.PropSummary)
		t.Notes, _ = p.Text(ical.PropDescription)
		if s, err := p.Text(ical.PropStatus); err == nil && s != "" {
			t.Status = strings.ToUpper(s)
		}
		if pr := p.Get(ical.PropPriority); pr != nil {
			if n, err := pr.Int(); err == nil {
				t.Priority = n
			}
		}
		if pr := p.Get(ical.PropDue); pr != nil {
			t.Due, _ = pr.DateTime(time.Local)
			t.DueAllDay = allDay(pr)
		}
		if pr := p.Get(ical.PropDateTimeStart); pr != nil {
			t.Start, _ = pr.DateTime(time.Local)
			t.StartAllDay = allDay(pr)
		}
		if pr := p.Get(ical.PropCompleted); pr != nil {
			t.Completed, _ = pr.DateTime(time.Local)
		}
		if pr := p.Get(ical.PropRecurrenceRule); pr != nil {
			t.RRule = pr.Value
		}
		for _, rel := range p.Values(ical.PropRelatedTo) {
			if rt := rel.Params.Get(ical.ParamRelationshipType); rt == "" || strings.EqualFold(rt, "PARENT") {
				t.Parent = rel.Value
			}
		}
		var buf bytes.Buffer
		if err := ical.NewEncoder(&buf).Encode(cal); err == nil {
			t.Raw = buf.String()
		}
		return t, true
	}
	return Task{}, false
}

// allDay reports whether a DUE/DTSTART value is a date rather than a date-time.
// Some clients omit VALUE=DATE, so fall back to the value's shape.
func allDay(p *ical.Prop) bool {
	return p.ValueType() == ical.ValueDate || len(p.Value) == 8
}
