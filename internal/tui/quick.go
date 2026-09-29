package tui

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/teambition/rrule-go"

	"github.com/chernesk/caldav-tui/internal/caldav"
)

// Quick is a task parsed from one line of free text.
type Quick struct {
	Title    string
	Due      caldav.When
	Start    caldav.When
	Priority int
	RRule    string
	Notes    string
}

var (
	prioTokRe = regexp.MustCompile(`^!(!{0,2}|[A-Za-z]+|[1-9])$`)
	bareFreq  = map[string]string{
		"daily": "FREQ=DAILY", "weekly": "FREQ=WEEKLY", "monthly": "FREQ=MONTHLY",
		"yearly": "FREQ=YEARLY", "annually": "FREQ=YEARLY", "weekdays": "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR",
	}
)

const maxPhrase = 5 // longest run of words tried as one date/repeat phrase

// QuickParse turns "call mom fri 5pm !high" into a task. Recognised, in any order:
//
//	!high !med !low !1..!9 !!!   priority
//	every week / every 2 weeks / every mon,wed / daily / weekdays   repeat
//	due:fri 2pm   start:oct 5    explicit dates (start is hidden until then)
//	... tomorrow 2pm             a date/time at the end of the title is the due date
//	... // some notes            everything after " // " is the notes
//
// Prefix a word with \ to keep it literally in the title (\!important).
func QuickParse(input string, now time.Time) (Quick, error) {
	var q Quick
	if i := strings.Index(input, " // "); i >= 0 {
		q.Notes = strings.TrimSpace(input[i+4:])
		input = input[:i]
	}
	toks := strings.Fields(input)
	used := make([]bool, len(toks))
	lit := func(i int) bool { return strings.HasPrefix(toks[i], `\`) }
	low := func(i int) string { return strings.ToLower(toks[i]) }

	// priority
	for i, t := range toks {
		if lit(i) || !prioTokRe.MatchString(t) {
			continue
		}
		if p, err := ParsePriority(t[1:]); err == nil && p != 0 || t == "!" {
			if t == "!" {
				p = 5
			} else if t == "!!" || t == "!!!" {
				p = 1
			}
			q.Priority, used[i] = p, true
		}
	}

	// due:/start: markers take the longest phrase that reads as a date
	dueHasDate := false
	for i := range toks {
		if used[i] || lit(i) {
			continue
		}
		var dst *caldav.When
		var pre string
		switch {
		case strings.HasPrefix(low(i), "due:"):
			dst, pre = &q.Due, "due:"
		case strings.HasPrefix(low(i), "start:"):
			dst, pre = &q.Start, "start:"
		case strings.HasPrefix(low(i), "starts:"):
			dst, pre = &q.Start, "starts:"
		default:
			continue
		}
		first := toks[i][len(pre):]
		n, w, hasDate, ok := longestWhen(toks, used, i, first, now)
		if !ok {
			return q, fmt.Errorf("can't read a date after %q", pre)
		}
		*dst = w
		if dst == &q.Due {
			dueHasDate = hasDate
		}
		used[i] = true
		for k := 1; k <= n; k++ {
			used[i+k] = true
		}
	}

	// "every ..." repeat phrase
	for i := range toks {
		if used[i] || lit(i) || low(i) != "every" || q.RRule != "" {
			continue
		}
		best, rule := 0, ""
		for k := 1; k <= maxPhrase-1 && i+k < len(toks) && !used[i+k]; k++ {
			if r, err := ParseRepeat("every " + strings.Join(toks[i+1:i+1+k], " ")); err == nil && r != "" {
				best, rule = k, r
			}
		}
		if best > 0 {
			q.RRule = rule
			for k := 0; k <= best; k++ {
				used[i+k] = true
			}
		}
	}

	rest := func() []int {
		var r []int
		for i := range toks {
			if !used[i] {
				r = append(r, i)
			}
		}
		return r
	}

	// bare frequency word ("water plants weekly", "standup weekdays 9:30am"): the last
	// one, as long as only a date phrase (or nothing) follows it
	if r := rest(); q.RRule == "" {
		for j := len(r) - 1; j >= 1; j-- {
			if lit(r[j]) {
				continue
			}
			rule, ok := bareFreq[low(r[j])]
			if !ok {
				continue
			}
			var after []string
			for _, i := range r[j+1:] {
				after = append(after, toks[i])
			}
			if len(after) > 0 {
				if w, _, err := parseWhen(strings.Join(after, " "), now); err != nil || w.IsZero() {
					continue
				}
			}
			q.RRule, used[r[j]] = rule, true
			break
		}
	}

	// trailing date phrase is the due date
	if r := rest(); q.Due.IsZero() {
		for k := min(maxPhrase, len(r)-1); k >= 1; k-- {
			tail := r[len(r)-k:]
			ok := true
			var words []string
			for _, i := range tail {
				if lit(i) {
					ok = false
				}
				words = append(words, toks[i])
			}
			if !ok {
				continue
			}
			if w, hasDate, err := parseWhen(strings.Join(words, " "), now); err == nil && !w.IsZero() {
				q.Due, dueHasDate = w, hasDate
				for _, i := range tail {
					used[i] = true
				}
				break
			}
		}
	}

	// a repeat needs a due date to advance from: use its first occurrence
	if q.RRule != "" && (q.Due.IsZero() || !dueHasDate) {
		if d, ok := firstOccurrence(q.RRule, now); ok {
			if q.Due.IsZero() {
				q.Due = caldav.When{T: d}
			} else {
				y, m, dd := d.Date()
				c := q.Due.T
				q.Due.T = time.Date(y, m, dd, c.Hour(), c.Minute(), 0, 0, time.UTC)
			}
		}
	}

	var title []string
	for i, t := range toks {
		if used[i] {
			continue
		}
		title = append(title, strings.TrimPrefix(t, `\`))
	}
	q.Title = strings.Join(title, " ")
	if q.Title == "" {
		return q, fmt.Errorf("type a title")
	}
	return q, nil
}

// longestWhen tries first plus 0..maxPhrase following unused words as one date
// phrase and returns how many following words it consumed.
func longestWhen(toks []string, used []bool, i int, first string, now time.Time) (int, caldav.When, bool, bool) {
	bestN, ok := -1, false
	var bestW caldav.When
	var bestHas bool
	for n := 0; n <= maxPhrase-1 && i+n < len(toks); n++ {
		if n > 0 && used[i+n] {
			break
		}
		phrase := strings.TrimSpace(first + " " + strings.Join(toks[i+1:i+1+n], " "))
		if w, has, err := parseWhen(phrase, now); err == nil && !w.IsZero() {
			bestN, bestW, bestHas, ok = n, w, has, true
		}
	}
	return bestN, bestW, bestHas, ok
}

// firstOccurrence is the first date on or after today that matches an RRULE.
func firstOccurrence(rule string, now time.Time) (time.Time, bool) {
	opt, err := rrule.StrToROption(rule)
	if err != nil {
		return time.Time{}, false
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	opt.Dtstart = today
	r, err := rrule.NewRRule(*opt)
	if err != nil {
		return time.Time{}, false
	}
	n := r.After(today.AddDate(0, 0, -1), false)
	return n, !n.IsZero()
}

// Describe summarises what was understood, for the live preview.
func (q Quick) Describe() string {
	parts := []string{q.Title}
	fmtW := func(w caldav.When) string {
		if w.HasTime {
			return w.T.Format("Mon Jan 2 15:04")
		}
		return w.T.Format("Mon Jan 2")
	}
	if !q.Due.IsZero() {
		parts = append(parts, "due "+fmtW(q.Due))
	}
	if !q.Start.IsZero() {
		parts = append(parts, "starts "+fmtW(q.Start))
	}
	if q.Priority != 0 {
		parts = append(parts, PriorityText(q.Priority))
	}
	if q.RRule != "" {
		parts = append(parts, "repeats "+RepeatText(q.RRule))
	}
	if q.Notes != "" {
		parts = append(parts, "+notes")
	}
	return strings.Join(parts, " · ")
}

// Edit converts the parsed task into field changes for creating it.
func (q Quick) Edit() caldav.Edit {
	e := caldav.Edit{Summary: &q.Title}
	if !q.Due.IsZero() {
		e.Due = &q.Due
	}
	if !q.Start.IsZero() {
		e.Start = &q.Start
	}
	if q.Priority != 0 {
		e.Priority = &q.Priority
	}
	if q.RRule != "" {
		e.RRule = &q.RRule
	}
	if q.Notes != "" {
		e.Notes = &q.Notes
	}
	return e
}
