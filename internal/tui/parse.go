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
	weekdays = map[string]time.Weekday{
		"sun": time.Sunday, "sunday": time.Sunday, "mon": time.Monday, "monday": time.Monday,
		"tue": time.Tuesday, "tues": time.Tuesday, "tuesday": time.Tuesday,
		"wed": time.Wednesday, "wednesday": time.Wednesday,
		"thu": time.Thursday, "thur": time.Thursday, "thurs": time.Thursday, "thursday": time.Thursday,
		"fri": time.Friday, "friday": time.Friday, "sat": time.Saturday, "saturday": time.Saturday,
	}
	months = map[string]time.Month{
		"jan": 1, "january": 1, "feb": 2, "february": 2, "mar": 3, "march": 3, "apr": 4, "april": 4,
		"may": 5, "jun": 6, "june": 6, "jul": 7, "july": 7, "aug": 8, "august": 8,
		"sep": 9, "sept": 9, "september": 9, "oct": 10, "october": 10, "nov": 11, "november": 11,
		"dec": 12, "december": 12,
	}
	timeRe   = regexp.MustCompile(`^(\d{1,2})(?::(\d{2}))?\s*(am|pm|a|p)?$`)
	isoRe    = regexp.MustCompile(`^(\d{4})-(\d{1,2})-(\d{1,2})$`)
	mdRe     = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})(?:/(\d{2,4}))?$`)
	relRe    = regexp.MustCompile(`^\+?(\d+)\s*(d|day|days|w|wk|wks|week|weeks|m|mo|month|months|y|yr|year|years)$`)
	dayNumRe = regexp.MustCompile(`^(\d{1,2})(?:st|nd|rd|th)?$`)
)

// ParseWhen understands things like "today", "tomorrow 2pm", "fri", "+3d",
// "oct 5", "5 oct 2027", "10/5", "2026-10-05 14:30". Empty input clears.
func ParseWhen(s string, now time.Time) (caldav.When, error) {
	w, _, err := parseWhen(s, now)
	return w, err
}

// parseWhen also reports whether the text named a date (as opposed to just a
// time of day, which defaults to today).
func parseWhen(s string, now time.Time) (caldav.When, bool, error) {
	toks := strings.Fields(strings.ToLower(strings.TrimSpace(s)))
	if len(toks) == 0 {
		return caldav.When{}, false, nil
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var date time.Time
	var clock *time.Duration
	setDate := func(t time.Time) error {
		if !date.IsZero() {
			return fmt.Errorf("two dates in %q", s)
		}
		date = t
		return nil
	}
	for i := 0; i < len(toks); i++ {
		tok := toks[i]
		// "at" and "in" are filler
		if tok == "at" || tok == "on" || tok == "in" || tok == "by" || tok == "due" || tok == "until" {
			continue
		}
		// glue "2 pm" back together
		if i+1 < len(toks) && (toks[i+1] == "am" || toks[i+1] == "pm") && timeRe.MatchString(tok) {
			tok += toks[i+1]
			i++
		}
		// glue "2 weeks" back together
		if i+1 < len(toks) && regexp.MustCompile(`^\+?\d+$`).MatchString(tok) && relRe.MatchString(tok+toks[i+1]) {
			tok += toks[i+1]
			i++
		}
		var err error
		switch {
		case tok == "noon":
			d := 12 * time.Hour
			clock = &d
		case tok == "midnight":
			d := time.Duration(0)
			clock = &d
		case tok == "today" || tok == "tod":
			err = setDate(today)
		case tok == "tomorrow" || tok == "tom" || tok == "tmrw":
			err = setDate(today.AddDate(0, 0, 1))
		case tok == "yesterday":
			err = setDate(today.AddDate(0, 0, -1))
		case isWeekday(tok):
			delta := (int(weekdays[tok]) - int(today.Weekday()) + 7) % 7
			if delta == 0 {
				delta = 7 // "mon" on a Monday means next Monday
			}
			err = setDate(today.AddDate(0, 0, delta))
		case relRe.MatchString(tok):
			m := relRe.FindStringSubmatch(tok)
			n, _ := strconv.Atoi(m[1])
			switch m[2][0] {
			case 'd':
				err = setDate(today.AddDate(0, 0, n))
			case 'w':
				err = setDate(today.AddDate(0, 0, 7*n))
			case 'm':
				err = setDate(today.AddDate(0, n, 0))
			case 'y':
				err = setDate(today.AddDate(n, 0, 0))
			}
		case isoRe.MatchString(tok):
			m := isoRe.FindStringSubmatch(tok)
			y, _ := strconv.Atoi(m[1])
			mo, _ := strconv.Atoi(m[2])
			d, _ := strconv.Atoi(m[3])
			err = setValidDate(setDate, y, mo, d)
		case mdRe.MatchString(tok):
			m := mdRe.FindStringSubmatch(tok)
			mo, _ := strconv.Atoi(m[1])
			d, _ := strconv.Atoi(m[2])
			y := 0
			if m[3] != "" {
				y, _ = strconv.Atoi(m[3])
				if y < 100 {
					y += 2000
				}
			}
			err = setMonthDay(setDate, today, y, mo, d)
		case months[tok] != 0:
			// "oct 5 [2027]" or "oct"
			mo := int(months[tok])
			d := 1
			if i+1 < len(toks) && dayNumRe.MatchString(toks[i+1]) {
				d, _ = strconv.Atoi(dayNumRe.FindStringSubmatch(toks[i+1])[1])
				i++
			}
			y := 0
			if i+1 < len(toks) && regexp.MustCompile(`^\d{4}$`).MatchString(toks[i+1]) {
				y, _ = strconv.Atoi(toks[i+1])
				i++
			}
			err = setMonthDay(setDate, today, y, mo, d)
		case dayNumRe.MatchString(tok) && i+1 < len(toks) && months[toks[i+1]] != 0:
			// "5 oct [2027]"
			d, _ := strconv.Atoi(dayNumRe.FindStringSubmatch(tok)[1])
			mo := int(months[toks[i+1]])
			i++
			y := 0
			if i+1 < len(toks) && regexp.MustCompile(`^\d{4}$`).MatchString(toks[i+1]) {
				y, _ = strconv.Atoi(toks[i+1])
				i++
			}
			err = setMonthDay(setDate, today, y, mo, d)
		case timeRe.MatchString(tok):
			d, ok := parseClock(tok)
			if !ok {
				return caldav.When{}, false, fmt.Errorf("bad time %q", tok)
			}
			clock = &d
		default:
			return caldav.When{}, false, fmt.Errorf("can't read %q", tok)
		}
		if err != nil {
			return caldav.When{}, false, err
		}
	}
	hasDate := !date.IsZero()
	if !hasDate {
		if clock == nil {
			return caldav.When{}, false, fmt.Errorf("no date in %q", s)
		}
		date = today // "3pm" alone means today
	}
	if clock != nil {
		return caldav.When{T: date.Add(*clock), HasTime: true}, hasDate, nil
	}
	return caldav.When{T: date}, hasDate, nil
}

func isWeekday(s string) bool { _, ok := weekdays[s]; return ok }

func setValidDate(set func(time.Time) error, y, mo, d int) error {
	t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
	if mo < 1 || mo > 12 || t.Day() != d {
		return fmt.Errorf("invalid date %04d-%02d-%02d", y, mo, d)
	}
	return set(t)
}

// setMonthDay resolves month/day; without a year it picks the next
// occurrence (this year unless that day has already passed).
func setMonthDay(set func(time.Time) error, today time.Time, y, mo, d int) error {
	if y != 0 {
		return setValidDate(set, y, mo, d)
	}
	t := time.Date(today.Year(), time.Month(mo), d, 0, 0, 0, 0, time.UTC)
	if mo < 1 || mo > 12 || t.Day() != d {
		return fmt.Errorf("invalid date %d/%d", mo, d)
	}
	if t.Before(today) {
		t = t.AddDate(1, 0, 0)
	}
	return set(t)
}

func parseClock(tok string) (time.Duration, bool) {
	m := timeRe.FindStringSubmatch(tok)
	if m == nil {
		return 0, false
	}
	h, _ := strconv.Atoi(m[1])
	min := 0
	if m[2] != "" {
		min, _ = strconv.Atoi(m[2])
	}
	switch m[3] {
	case "am", "a":
		if h < 1 || h > 12 {
			return 0, false
		}
		if h == 12 {
			h = 0
		}
	case "pm", "p":
		if h < 1 || h > 12 {
			return 0, false
		}
		if h != 12 {
			h += 12
		}
	default:
		// bare number without ':' is ambiguous with a day number; require a colon
		if m[2] == "" {
			return 0, false
		}
	}
	if h > 23 || min > 59 {
		return 0, false
	}
	return time.Duration(h)*time.Hour + time.Duration(min)*time.Minute, true
}

// FormatWhen renders a When back into text ParseWhen accepts.
func FormatWhen(w caldav.When) string {
	switch {
	case w.IsZero():
		return ""
	case w.HasTime:
		return w.T.Format("2006-01-02 15:04")
	}
	return w.T.Format("2006-01-02")
}

// ParsePriority accepts high/medium/low/none, h/m/l, 1-9, or "!!!"-style.
func ParsePriority(s string) (int, error) {
	switch v := strings.ToLower(strings.TrimSpace(s)); v {
	case "", "none", "no", "n", "-", "0":
		return 0, nil
	case "high", "hi", "h", "!!!", "!!":
		return 1, nil
	case "medium", "med", "mid", "m", "!":
		return 5, nil
	case "low", "lo", "l":
		return 9, nil
	default:
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 9 {
			return n, nil
		}
		return 0, fmt.Errorf("priority: high, medium, low, none, or 1-9")
	}
}

// PriorityText is the inverse of ParsePriority for prefilling the form.
func PriorityText(p int) string {
	switch {
	case p == 0:
		return ""
	case p <= 4:
		return "high"
	case p == 5:
		return "medium"
	default:
		return "low"
	}
}

var (
	everyRe   = regexp.MustCompile(`^(?:every\s+)?(\d+|other)\s*(day|days|week|weeks|month|months|year|years)$`)
	freqWords = map[string]string{
		"daily": "DAILY", "day": "DAILY", "weekly": "WEEKLY", "week": "WEEKLY",
		"monthly": "MONTHLY", "month": "MONTHLY", "yearly": "YEARLY", "year": "YEARLY", "annually": "YEARLY",
	}
	byDay = map[time.Weekday]string{
		time.Sunday: "SU", time.Monday: "MO", time.Tuesday: "TU", time.Wednesday: "WE",
		time.Thursday: "TH", time.Friday: "FR", time.Saturday: "SA",
	}
	dayCodes = map[string]time.Weekday{}
)

func init() {
	for w, c := range byDay {
		dayCodes[c] = w
	}
}

// ParseRepeat turns "daily", "weekly mon,wed", "every 2 weeks", "weekdays" or a raw
// "FREQ=..." rule into an RRULE value. Empty clears.
func ParseRepeat(s string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	if v == "" || v == "none" || v == "never" {
		return "", nil
	}
	if strings.HasPrefix(v, "freq=") {
		return strings.ToUpper(strings.TrimSpace(s)), nil
	}
	if v == "weekdays" || v == "every weekday" {
		return "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR", nil
	}
	if m := everyRe.FindStringSubmatch(v); m != nil {
		n := 2 // "every other week"
		if m[1] != "other" {
			n, _ = strconv.Atoi(m[1])
		}
		if n < 1 {
			return "", fmt.Errorf("repeat interval must be at least 1")
		}
		f := freqWords[strings.TrimSuffix(m[2], "s")]
		if n == 1 {
			return "FREQ=" + f, nil
		}
		return fmt.Sprintf("FREQ=%s;INTERVAL=%d", f, n), nil
	}
	// "daily", "weekly", "every week", optionally followed by weekdays: "weekly mon,wed"
	rest := strings.TrimPrefix(v, "every ")
	first, days, _ := strings.Cut(rest, " ")
	if f, ok := freqWords[first]; ok && days == "" {
		return "FREQ=" + f, nil
	} else if ok && f == "WEEKLY" {
		codes, err := parseDays(days)
		if err != nil {
			return "", err
		}
		return "FREQ=WEEKLY;BYDAY=" + codes, nil
	}
	// "every mon,wed,fri"
	if strings.HasPrefix(v, "every ") {
		if codes, err := parseDays(rest); err == nil {
			return "FREQ=WEEKLY;BYDAY=" + codes, nil
		}
	}
	return "", fmt.Errorf("repeat: daily, weekly [mon,wed], monthly, yearly, every N weeks, weekdays")
}

func parseDays(s string) (string, error) {
	var codes []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '/' }) {
		w, ok := weekdays[p]
		if !ok {
			return "", fmt.Errorf("unknown weekday %q", p)
		}
		codes = append(codes, byDay[w])
	}
	if len(codes) == 0 {
		return "", fmt.Errorf("no weekdays given")
	}
	return strings.Join(codes, ","), nil
}

// RepeatText renders an RRULE the way ParseRepeat reads it, falling back to the
// raw rule for anything more exotic so it round-trips unchanged.
func RepeatText(rrule string) string {
	if rrule == "" {
		return ""
	}
	parts := map[string]string{}
	for _, p := range strings.Split(rrule, ";") {
		if k, v, ok := strings.Cut(p, "="); ok {
			parts[strings.ToUpper(k)] = v
		}
	}
	for k := range parts {
		if k != "FREQ" && k != "INTERVAL" && k != "BYDAY" {
			return rrule // COUNT/UNTIL/BYMONTHDAY/...: keep raw
		}
	}
	unit := map[string]string{"DAILY": "day", "WEEKLY": "week", "MONTHLY": "month", "YEARLY": "year"}[parts["FREQ"]]
	if unit == "" {
		return rrule
	}
	iv := 1
	if parts["INTERVAL"] != "" {
		iv, _ = strconv.Atoi(parts["INTERVAL"])
	}
	if by := parts["BYDAY"]; by != "" {
		if parts["FREQ"] != "WEEKLY" || iv != 1 {
			return rrule
		}
		var names []string
		for _, c := range strings.Split(by, ",") {
			w, ok := dayCodes[c]
			if !ok {
				return rrule // e.g. 2MO
			}
			names = append(names, strings.ToLower(w.String()[:3]))
		}
		if strings.Join(names, ",") == "mon,tue,wed,thu,fri" {
			return "weekdays"
		}
		return "weekly " + strings.Join(names, ",")
	}
	if iv == 1 {
		return map[string]string{"day": "daily", "week": "weekly", "month": "monthly", "year": "yearly"}[unit]
	}
	return fmt.Sprintf("every %d %ss", iv, unit)
}
