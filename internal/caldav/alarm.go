package caldav

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Alarm is a relative VALARM trigger: Offset from the task's start, or from its
// due date when End is set (negative Offset = before). Only timing exists on the
// server: clients like Tasks.org keep the alert mode (once / repeating /
// insistent) locally.
type Alarm struct {
	Offset time.Duration
	End    bool
}

func (a Alarm) related() string {
	if a.End {
		return "due"
	}
	return "start"
}

// String reads like "at due", "1 day after due", "10 min before start".
func (a Alarm) String() string {
	if a.Offset == 0 {
		return "at " + a.related()
	}
	d, dir := a.Offset, "after"
	if d < 0 {
		d, dir = -d, "before"
	}
	var parts []string
	add := func(v int64, unit string) {
		if v == 0 {
			return
		}
		if v != 1 && unit != "min" {
			unit += "s"
		}
		parts = append(parts, fmt.Sprintf("%d %s", v, unit))
	}
	secs := int64(d / time.Second)
	add(secs/86400, "day")
	add(secs%86400/3600, "hour")
	add(secs%3600/60, "min")
	add(secs%60, "sec")
	return strings.Join(parts, " ") + " " + dir + " " + a.related()
}

// trigger renders the TRIGGER property line.
func (a Alarm) trigger() string {
	rel := "START"
	if a.End {
		rel = "END"
	}
	d, sign := a.Offset, ""
	if d < 0 {
		d, sign = -d, "-"
	}
	secs := int64(d / time.Second)
	out := sign + "P"
	if days := secs / 86400; days > 0 {
		out += strconv.FormatInt(days, 10) + "D"
	}
	if rem := secs % 86400; rem > 0 || secs == 0 {
		out += "T"
		if h := rem / 3600; h > 0 {
			out += strconv.FormatInt(h, 10) + "H"
		}
		if m := rem % 3600 / 60; m > 0 {
			out += strconv.FormatInt(m, 10) + "M"
		}
		if s := rem % 60; s > 0 || secs == 0 {
			out += strconv.FormatInt(s, 10) + "S"
		}
	}
	return "TRIGGER;RELATED=" + rel + ":" + out
}

// block renders a complete VALARM, in the form Tasks.org writes.
func (a Alarm) block() []string {
	return []string{
		"BEGIN:VALARM", "ACTION:DISPLAY", "DESCRIPTION:Default Tasks.org description",
		a.trigger(), "END:VALARM",
	}
}

var durRe = regexp.MustCompile(`^([+-])?P(?:(\d+)W)?(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

// parseTrigger reads a TRIGGER line. ok is false for absolute (DATE-TIME)
// triggers and anything unreadable; abs then holds a display form.
func parseTrigger(line string) (a Alarm, abs string, ok bool) {
	name, val, _ := strings.Cut(line, ":")
	absolute := false
	for _, p := range strings.Split(name, ";")[1:] {
		k, v, _ := strings.Cut(p, "=")
		switch strings.ToUpper(k) {
		case "RELATED":
			a.End = strings.EqualFold(v, "END")
		case "VALUE":
			absolute = strings.EqualFold(v, "DATE-TIME")
		}
	}
	if absolute {
		if tm, err := time.Parse("20060102T150405Z", val); err == nil {
			return a, tm.Local().Format("Mon Jan 2 3:04pm"), false
		}
		return a, val, false
	}
	m := durRe.FindStringSubmatch(val)
	if m == nil {
		return a, val, false
	}
	n := func(s string) int64 { v, _ := strconv.ParseInt(s, 10, 64); return v }
	secs := n(m[2])*7*86400 + n(m[3])*86400 + n(m[4])*3600 + n(m[5])*60 + n(m[6])
	a.Offset = time.Duration(secs) * time.Second
	if m[1] == "-" {
		a.Offset = -a.Offset
	}
	return a, "", true
}

// alarmTriggers returns the TRIGGER line of each VALARM in raw.
func alarmTriggers(raw string) []string {
	var out []string
	in := false
	var trigger string
	for _, line := range unfold(raw) {
		switch {
		case strings.EqualFold(line, "BEGIN:VALARM"):
			in, trigger = true, ""
		case strings.EqualFold(line, "END:VALARM"):
			if in && trigger != "" {
				out = append(out, trigger)
			}
			in = false
		case in && strings.HasPrefix(strings.ToUpper(line), "TRIGGER"):
			trigger = line
		}
	}
	return out
}

// Alarms describes every VALARM trigger on the task in words, absolute ones included.
func (t Task) Alarms() []string {
	var out []string
	for _, tr := range alarmTriggers(t.Raw) {
		if a, abs, ok := parseTrigger(tr); ok {
			out = append(out, a.String())
		} else {
			out = append(out, abs)
		}
	}
	return out
}

// RelativeAlarms returns the alarms an Edit can change: relative triggers only.
func (t Task) RelativeAlarms() []Alarm {
	var out []Alarm
	for _, tr := range alarmTriggers(t.Raw) {
		if a, _, ok := parseTrigger(tr); ok {
			out = append(out, a)
		}
	}
	return out
}

// setAlarms makes the relative alarms exactly want. Existing alarms that are
// still wanted are kept as they are (extra properties and all); absolute
// alarms are never touched.
func (d *doc) setAlarms(want []Alarm) {
	left := append([]Alarm(nil), want...)
	out := d.items[:0:0]
	for _, it := range d.items {
		if it.name == "" && len(it.lines) > 0 && strings.EqualFold(it.lines[0], "BEGIN:VALARM") {
			var a Alarm
			rel := false
			for _, l := range it.lines[1:] {
				if strings.HasPrefix(strings.ToUpper(l), "TRIGGER") {
					a, _, rel = parseTrigger(l)
				}
			}
			if rel {
				keep := false
				for i, w := range left {
					if w == a {
						left = append(left[:i], left[i+1:]...)
						keep = true
						break
					}
				}
				if !keep {
					continue
				}
			}
		}
		out = append(out, it)
	}
	for _, a := range left {
		out = append(out, item{lines: a.block()})
	}
	d.items = out
}
