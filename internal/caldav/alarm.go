package caldav

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Alarms describes each VALARM trigger on the task in words ("at due",
// "1 day after due", "10 min before start"). Only timing is available: clients
// like Tasks.org keep the alert mode (once / repeating / insistent) locally.
func (t Task) Alarms() []string {
	var out []string
	in := false
	var trigger string
	// Unfold continuation lines first.
	raw := strings.ReplaceAll(t.Raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\n ", "")
	raw = strings.ReplaceAll(raw, "\n\t", "")
	for _, line := range strings.Split(raw, "\n") {
		switch {
		case line == "BEGIN:VALARM":
			in, trigger = true, ""
		case line == "END:VALARM":
			if in && trigger != "" {
				out = append(out, describeTrigger(trigger))
			}
			in = false
		case in && strings.HasPrefix(line, "TRIGGER"):
			trigger = line
		}
	}
	return out
}

var durRe = regexp.MustCompile(`^([+-])?P(?:(\d+)W)?(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

func describeTrigger(line string) string {
	name, val, _ := strings.Cut(line, ":")
	params := strings.Split(name, ";")[1:]
	related := "start"
	absolute := false
	for _, p := range params {
		k, v, _ := strings.Cut(p, "=")
		switch strings.ToUpper(k) {
		case "RELATED":
			if strings.EqualFold(v, "END") {
				related = "due"
			}
		case "VALUE":
			absolute = strings.EqualFold(v, "DATE-TIME")
		}
	}
	if absolute {
		if tm, err := time.Parse("20060102T150405Z", val); err == nil {
			return tm.Local().Format("Mon Jan 2 3:04pm")
		}
		return val
	}
	m := durRe.FindStringSubmatch(val)
	if m == nil {
		return val
	}
	n := func(s string) int { v, _ := strconv.Atoi(s); return v }
	var parts []string
	add := func(v int, unit string) {
		if v == 0 {
			return
		}
		if v != 1 && unit != "min" {
			unit += "s"
		}
		parts = append(parts, fmt.Sprintf("%d %s", v, unit))
	}
	add(n(m[2]), "week")
	add(n(m[3]), "day")
	add(n(m[4]), "hour")
	add(n(m[5]), "min")
	add(n(m[6]), "sec")
	if len(parts) == 0 {
		return "at " + related
	}
	dir := "after"
	if m[1] == "-" {
		dir = "before"
	}
	return strings.Join(parts, " ") + " " + dir + " " + related
}
