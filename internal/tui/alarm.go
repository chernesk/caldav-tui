package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chernesk/caldav-tui/internal/caldav"
)

var (
	atRe   = regexp.MustCompile(`^(?:at|when|on)?\s*(due|start|started|starts)$`)
	pairRe = regexp.MustCompile(`(\d+)\s*([a-z]+)`)
	onlyRe = regexp.MustCompile(`^(?:\s*\d+\s*[a-z]+\s*)+$`)
	unitD  = map[string]time.Duration{
		"s": time.Second, "sec": time.Second, "secs": time.Second, "second": time.Second, "seconds": time.Second,
		"m": time.Minute, "min": time.Minute, "mins": time.Minute, "minute": time.Minute, "minutes": time.Minute,
		"h": time.Hour, "hr": time.Hour, "hrs": time.Hour, "hour": time.Hour, "hours": time.Hour,
		"d": 24 * time.Hour, "day": 24 * time.Hour, "days": 24 * time.Hour,
		"w": 7 * 24 * time.Hour, "week": 7 * 24 * time.Hour, "weeks": 7 * 24 * time.Hour,
	}
)

// ParseAlarm reads one alarm: "at due", "at start", "10 min before", "1 day after due",
// "2h before start", "1 day 2 hours before". Without before/after a duration means before;
// without due/start the alarm is relative to the due date.
func ParseAlarm(s string) (caldav.Alarm, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	var a caldav.Alarm
	bad := fmt.Errorf("alarm %q: try at due, at start, 10 min before, 1 day after due, 2h before start", s)
	if m := atRe.FindStringSubmatch(v); m != nil {
		a.End = m[1] == "due"
		return a, nil
	}
	a.End = true
	fields := strings.Fields(v)
	if n := len(fields); n > 0 {
		switch fields[n-1] {
		case "due":
			fields = fields[:n-1]
		case "start", "started", "starts":
			a.End = false
			fields = fields[:n-1]
		}
	}
	sign := time.Duration(-1)
	if n := len(fields); n > 0 {
		switch fields[n-1] {
		case "before", "prior":
			fields = fields[:n-1]
		case "after", "past":
			sign = 1
			fields = fields[:n-1]
		}
	}
	body := strings.Join(fields, " ")
	if !onlyRe.MatchString(body) {
		return a, bad
	}
	var total time.Duration
	for _, p := range pairRe.FindAllStringSubmatch(body, -1) {
		unit, ok := unitD[p[2]]
		n, err := strconv.Atoi(p[1])
		if !ok || err != nil {
			return a, bad
		}
		total += time.Duration(n) * unit
	}
	a.Offset = sign * total
	return a, nil
}

// ParseAlarms reads a comma-separated list of alarms; empty or "none" means no alarms.
func ParseAlarms(s string) ([]caldav.Alarm, error) {
	v := strings.TrimSpace(s)
	if v == "" || strings.EqualFold(v, "none") {
		return []caldav.Alarm{}, nil
	}
	var out []caldav.Alarm
	for _, part := range strings.Split(v, ",") {
		a, err := ParseAlarm(part)
		if err != nil {
			return nil, err
		}
		if !slicesContains(out, a) {
			out = append(out, a)
		}
	}
	return out, nil
}

func slicesContains(as []caldav.Alarm, a caldav.Alarm) bool {
	for _, x := range as {
		if x == a {
			return true
		}
	}
	return false
}

// AlarmsText renders alarms the way ParseAlarms reads them.
func AlarmsText(as []caldav.Alarm) string {
	s := make([]string, len(as))
	for i, a := range as {
		s[i] = a.String()
	}
	return strings.Join(s, ", ")
}
