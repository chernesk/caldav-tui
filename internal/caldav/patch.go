package caldav

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/teambition/rrule-go"
)

// The functions here edit iCalendar text in place. Only the properties we
// manage are rewritten; everything else on the VTODO (alarms, categories,
// X- properties from other clients, ...) is carried through verbatim.

// When is a date or date-time. HasTime=false means an all-day (VALUE=DATE)
// value. The zero When means "no value" (clear the property).
type When struct {
	T       time.Time
	HasTime bool
}

func (w When) IsZero() bool { return w.T.IsZero() }

// Edit lists property changes. A nil field is left untouched, which matters:
// an unchanged DUE keeps its original TZID/UTC form instead of being reformatted.
type Edit struct {
	Summary  *string
	Notes    *string
	Due      *When // zero When clears
	Start    *When // zero When clears
	Priority *int  // 0 clears
	RRule    *string
}

type item struct {
	name  string   // upper-case property name; "" for a nested component block
	lines []string // unfolded lines
}

type doc struct {
	pre, post []string // lines before BEGIN:VTODO (inclusive) / from END:VTODO on
	items     []item   // top-level contents of the first VTODO
}

func unfold(raw string) []string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	raw = strings.ReplaceAll(raw, "\n ", "")
	raw = strings.ReplaceAll(raw, "\n\t", "")
	lines := strings.Split(raw, "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// splitProp splits "NAME;PARAM=x:value" into name, params (with leading ';')
// and value, ignoring colons inside quoted parameter values.
func splitProp(line string) (name, params, value string) {
	inQ := false
	end := -1
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			inQ = !inQ
		case ':':
			if !inQ {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return strings.ToUpper(line), "", ""
	}
	head, value := line[:end], line[end+1:]
	if i := strings.IndexByte(head, ';'); i >= 0 {
		return strings.ToUpper(head[:i]), head[i:], value
	}
	return strings.ToUpper(head), "", value
}

func parseDoc(raw string) (*doc, error) {
	lines := unfold(raw)
	d := &doc{}
	i, found := 0, false
	for ; i < len(lines) && !found; i++ {
		d.pre = append(d.pre, lines[i])
		found = strings.EqualFold(lines[i], "BEGIN:VTODO")
	}
	if !found {
		return nil, fmt.Errorf("no VTODO in calendar object")
	}
	for ; i < len(lines); i++ {
		l := lines[i]
		if strings.EqualFold(l, "END:VTODO") {
			break
		}
		if up := strings.ToUpper(l); strings.HasPrefix(up, "BEGIN:") {
			block := []string{l}
			depth := 1
			for i++; i < len(lines) && depth > 0; i++ {
				block = append(block, lines[i])
				u := strings.ToUpper(lines[i])
				if strings.HasPrefix(u, "BEGIN:") {
					depth++
				} else if strings.HasPrefix(u, "END:") {
					depth--
				}
			}
			i-- // the loop's i++ moves past the block's END line
			d.items = append(d.items, item{lines: block})
			continue
		}
		name, _, _ := splitProp(l)
		d.items = append(d.items, item{name: name, lines: []string{l}})
	}
	d.post = lines[min(i, len(lines)):]
	return d, nil
}

func (d *doc) render() string {
	var b strings.Builder
	w := func(l string) { b.WriteString(fold(l)); b.WriteString("\r\n") }
	for _, l := range d.pre {
		w(l)
	}
	for _, it := range d.items {
		for _, l := range it.lines {
			w(l)
		}
	}
	for _, l := range d.post {
		w(l)
	}
	return b.String()
}

func (d *doc) prop(name string) (string, bool) {
	for _, it := range d.items {
		if it.name == name {
			return it.lines[0], true
		}
	}
	return "", false
}

// set replaces the first occurrence of a property (dropping duplicates), or
// inserts it ahead of any nested components such as VALARM.
func (d *doc) set(name, line string) {
	out := d.items[:0:0]
	done := false
	for _, it := range d.items {
		if it.name == name {
			if !done {
				out = append(out, item{name: name, lines: []string{line}})
				done = true
			}
			continue
		}
		out = append(out, it)
	}
	if !done {
		at := len(out)
		for i, it := range out {
			if it.name == "" {
				at = i
				break
			}
		}
		out = append(out, item{})
		copy(out[at+1:], out[at:])
		out[at] = item{name: name, lines: []string{line}}
	}
	d.items = out
}

func (d *doc) del(name string) {
	out := d.items[:0:0]
	for _, it := range d.items {
		if it.name != name {
			out = append(out, it)
		}
	}
	d.items = out
}

// fold wraps a line at 75 octets without splitting a UTF-8 sequence.
func fold(line string) string {
	if len(line) <= 75 {
		return line
	}
	var b strings.Builder
	limit := 75
	n := 0
	for _, r := range line {
		sz := len(string(r))
		if n+sz > limit {
			b.WriteString("\r\n ")
			n, limit = 1, 75 // continuation lines start with a space
		}
		b.WriteRune(r)
		n += sz
	}
	return b.String()
}

func escapeText(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, ";", "\\;")
	s = strings.ReplaceAll(s, ",", "\\,")
	s = strings.ReplaceAll(s, "\r\n", "\\n")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return strings.ReplaceAll(s, "\r", "")
}

const (
	icsDate     = "20060102"
	icsDateTime = "20060102T150405"
)

// dateLine renders a When. Times are written floating (no TZID), like the
// Chrome extension, so they mean the same wall-clock time in every client.
func dateLine(name string, w When) string {
	if w.HasTime {
		return name + ":" + w.T.Format(icsDateTime)
	}
	return name + ";VALUE=DATE:" + w.T.Format(icsDate)
}

func isDateOnly(line string) bool {
	_, params, val := splitProp(line)
	up := strings.ToUpper(params)
	if strings.Contains(up, "VALUE=DATE-TIME") {
		return false
	}
	return strings.Contains(up, "VALUE=DATE") || len(val) == 8
}

// wallClock parses an iCalendar date/date-time value as a naive UTC time so
// recurrence arithmetic works on the wall-clock reading, ignoring TZID/Z.
func wallClock(val string) (time.Time, bool) {
	val = strings.TrimSuffix(val, "Z")
	if t, err := time.Parse(icsDateTime, val); err == nil {
		return t, true
	}
	if t, err := time.Parse(icsDate, val); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func stamp(t time.Time) string { return t.UTC().Format("20060102T150405Z") }

func (d *doc) apply(e Edit, now time.Time) {
	if e.Summary != nil {
		d.set("SUMMARY", "SUMMARY:"+escapeText(*e.Summary))
	}
	if e.Notes != nil {
		if *e.Notes == "" {
			d.del("DESCRIPTION")
		} else {
			d.set("DESCRIPTION", "DESCRIPTION:"+escapeText(*e.Notes))
		}
	}
	for _, f := range []struct {
		name string
		w    *When
	}{{"DUE", e.Due}, {"DTSTART", e.Start}} {
		if f.w == nil {
			continue
		}
		if f.w.IsZero() {
			d.del(f.name)
		} else {
			d.set(f.name, dateLine(f.name, *f.w))
		}
	}
	if e.Priority != nil {
		if *e.Priority == 0 {
			d.del("PRIORITY")
		} else {
			d.set("PRIORITY", "PRIORITY:"+strconv.Itoa(*e.Priority))
		}
	}
	if e.RRule != nil {
		if *e.RRule == "" {
			d.del("RRULE")
		} else {
			d.set("RRULE", "RRULE:"+*e.RRule)
		}
	}
	d.matchStartToDue()
	d.touch(now)
}

// matchStartToDue: DUE and DTSTART must share a value type (Fastmail rejects a
// mismatch), so DTSTART follows DUE's type.
func (d *doc) matchStartToDue() {
	due, ok1 := d.prop("DUE")
	start, ok2 := d.prop("DTSTART")
	if !ok1 || !ok2 || isDateOnly(due) == isDateOnly(start) {
		return
	}
	_, _, sv := splitProp(start)
	if len(sv) < 8 {
		return
	}
	if isDateOnly(due) {
		d.set("DTSTART", "DTSTART;VALUE=DATE:"+sv[:8])
	} else {
		d.set("DTSTART", "DTSTART:"+sv[:8]+"T000000")
	}
}

func (d *doc) touch(now time.Time) {
	d.set("DTSTAMP", "DTSTAMP:"+stamp(now))
	d.set("LAST-MODIFIED", "LAST-MODIFIED:"+stamp(now))
}

// ApplyEdit patches raw with e and returns the new iCalendar text.
func ApplyEdit(raw string, e Edit, now time.Time) (string, error) {
	d, err := parseDoc(raw)
	if err != nil {
		return "", err
	}
	d.apply(e, now)
	return d.render(), nil
}

// NewRaw builds a fresh VTODO with the given fields. parent is the UID of a
// parent task ("" for none).
func NewRaw(uid, parent string, e Edit, now time.Time) (string, error) {
	lines := []string{
		"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//caldav-tui//EN",
		"BEGIN:VTODO", "UID:" + uid, "CREATED:" + stamp(now), "STATUS:NEEDS-ACTION",
	}
	if parent != "" {
		lines = append(lines, "RELATED-TO:"+parent)
	}
	lines = append(lines, "END:VTODO", "END:VCALENDAR")
	d, err := parseDoc(strings.Join(lines, "\r\n"))
	if err != nil {
		return "", err
	}
	d.apply(e, now)
	return d.render(), nil
}

// SetDone marks the task complete, or reopens it when done is false.
func SetDone(raw string, done bool, now time.Time) (string, error) {
	d, err := parseDoc(raw)
	if err != nil {
		return "", err
	}
	d.setDone(done, now)
	d.touch(now)
	return d.render(), nil
}

func (d *doc) setDone(done bool, now time.Time) {
	if done {
		d.set("STATUS", "STATUS:COMPLETED")
		d.set("COMPLETED", "COMPLETED:"+stamp(now))
		d.set("PERCENT-COMPLETE", "PERCENT-COMPLETE:100")
		return
	}
	d.set("STATUS", "STATUS:NEEDS-ACTION")
	d.del("COMPLETED")
	d.del("PERCENT-COMPLETE")
}

// CompleteResult says what Complete did.
type CompleteResult int

const (
	Completed CompleteResult = iota // task marked COMPLETED
	Advanced                        // recurring: moved to the next occurrence
)

var countRe = regexp.MustCompile(`(?i)COUNT=\d+`)

// Complete finishes a task. A recurring task is not completed: its DUE (and
// DTSTART, by the same offset) advance to the next occurrence and it stays open,
// like tasks.org. Only when the rule is exhausted (or can't be evaluated) is it
// really marked COMPLETED.
func Complete(raw string, now time.Time) (string, CompleteResult, error) {
	d, err := parseDoc(raw)
	if err != nil {
		return "", Completed, err
	}
	if d.advance() {
		d.touch(now)
		return d.render(), Advanced, nil
	}
	d.setDone(true, now)
	d.touch(now)
	return d.render(), Completed, nil
}

func (d *doc) advance() bool {
	rl, ok := d.prop("RRULE")
	if !ok {
		return false
	}
	_, _, rule := splitProp(rl)
	baseName := "DUE"
	baseLine, ok := d.prop("DUE")
	if !ok {
		baseName = "DTSTART"
		if baseLine, ok = d.prop("DTSTART"); !ok {
			return false
		}
	}
	bn, bparams, bval := splitProp(baseLine)
	base, ok := wallClock(bval)
	if !ok {
		return false
	}

	opt, err := rrule.StrToROption(rule)
	if err != nil {
		return false
	}
	count := opt.Count
	if count == 1 {
		return false // that was the last occurrence
	}
	opt.Count = 0 // COUNT is relative to DTSTART, which we're moving; track it ourselves
	opt.Dtstart = base
	r, err := rrule.NewRRule(*opt)
	if err != nil {
		return false
	}
	next := r.After(base, false)
	if next.IsZero() {
		return false // UNTIL passed
	}

	d.set(baseName, bn+bparams+":"+next.Format(icsDate)+bval[min(8, len(bval)):])
	if baseName == "DUE" {
		if sl, ok := d.prop("DTSTART"); ok {
			sn, sparams, sval := splitProp(sl)
			if s, ok := wallClock(sval); ok {
				days := int(math.Round(next.Sub(base).Hours() / 24))
				d.set("DTSTART", sn+sparams+":"+s.AddDate(0, 0, days).Format(icsDate)+sval[min(8, len(sval)):])
			}
		}
	}
	if count > 1 {
		d.set("RRULE", "RRULE:"+countRe.ReplaceAllString(rule, "COUNT="+strconv.Itoa(count-1)))
	}
	d.setDone(false, time.Time{})
	return true
}
