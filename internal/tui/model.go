// Package tui is the Bubble Tea front end. It only ever reads the local cache;
// sync runs in the background and the list reloads when it finishes.
package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/chernesk/caldav-tui/internal/caldav"
	"github.com/chernesk/caldav-tui/internal/store"
)

var (
	dim      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	bold     = lipgloss.NewStyle().Bold(true)
	selected = lipgloss.NewStyle().Reverse(true)
	overdue  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	today    = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	prioHigh = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	prioMed  = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	prioLow  = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	title    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	errStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)

type row struct {
	task  caldav.Task
	depth int
}

type Model struct {
	cl *caldav.Client
	st *store.Store

	cals     []caldav.Calendar
	calIdx   int // -1 = all calendars
	rows     []row
	cursor   int
	offset   int
	showDone bool

	syncing  bool
	lastSync string
	err      error

	w, h int
}

func New(cl *caldav.Client, st *store.Store) Model {
	m := Model{cl: cl, st: st, calIdx: -1, syncing: true}
	m.reload()
	return m
}

type syncDone struct{ err error }

func (m Model) sync() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		return syncDone{Sync(ctx, m.cl, m.st)}
	}
}

func (m Model) Init() tea.Cmd { return m.sync() }

func (m *Model) reload() {
	m.cals, _ = m.st.Calendars()
	if m.calIdx >= len(m.cals) {
		m.calIdx = -1
	}
	cal := ""
	if m.calIdx >= 0 {
		cal = m.cals[m.calIdx].Path
	}
	tasks, err := m.st.Tasks(cal)
	if err != nil {
		m.err = err
		return
	}
	var keep []caldav.Task
	for _, t := range tasks {
		if t.Done() && !m.showDone {
			continue
		}
		keep = append(keep, t)
	}
	m.rows = buildTree(keep)
	if m.cursor >= len(m.rows) {
		m.cursor = max(0, len(m.rows)-1)
	}
	m.lastSync = m.st.Meta("last_sync")
}

// less orders open tasks first, then by due date (undated last), priority, summary.
func less(a, b caldav.Task) bool {
	if a.Done() != b.Done() {
		return !a.Done()
	}
	if a.Due.IsZero() != b.Due.IsZero() {
		return !a.Due.IsZero()
	}
	if !a.Due.Equal(b.Due) {
		return a.Due.Before(b.Due)
	}
	pa, pb := prioRank(a.Priority), prioRank(b.Priority)
	if pa != pb {
		return pa < pb
	}
	return strings.ToLower(a.Summary) < strings.ToLower(b.Summary)
}

func prioRank(p int) int {
	if p == 0 {
		return 10
	}
	return p
}

// buildTree orders tasks with subtasks nested under their RELATED-TO parent.
// A task whose parent isn't in the visible set is shown as a root.
func buildTree(tasks []caldav.Task) []row {
	byUID := map[string]bool{}
	for _, t := range tasks {
		byUID[t.UID] = true
	}
	kids := map[string][]caldav.Task{}
	var roots []caldav.Task
	for _, t := range tasks {
		if t.Parent != "" && t.Parent != t.UID && byUID[t.Parent] {
			kids[t.Parent] = append(kids[t.Parent], t)
		} else {
			roots = append(roots, t)
		}
	}
	sort.SliceStable(roots, func(i, j int) bool { return less(roots[i], roots[j]) })
	var out []row
	seen := map[string]bool{}
	var walk func(t caldav.Task, depth int)
	walk = func(t caldav.Task, depth int) {
		if seen[t.UID] {
			return // guards against parent cycles
		}
		seen[t.UID] = true
		out = append(out, row{t, depth})
		c := kids[t.UID]
		sort.SliceStable(c, func(i, j int) bool { return less(c[i], c[j]) })
		for _, k := range c {
			walk(k, depth+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	return out
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case syncDone:
		m.syncing = false
		m.err = msg.err
		m.reload()
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "j", "down":
			m.move(1)
		case "k", "up":
			m.move(-1)
		case "ctrl+d", "pgdown":
			m.move(m.listHeight() / 2)
		case "ctrl+u", "pgup":
			m.move(-m.listHeight() / 2)
		case "g", "home":
			m.cursor = 0
		case "G", "end":
			m.cursor = max(0, len(m.rows)-1)
		case "tab":
			m.calIdx++
			if m.calIdx >= len(m.cals) {
				m.calIdx = -1
			}
			m.cursor, m.offset = 0, 0
			m.reload()
		case "shift+tab":
			m.calIdx--
			if m.calIdx < -1 {
				m.calIdx = len(m.cals) - 1
			}
			m.cursor, m.offset = 0, 0
			m.reload()
		case "c":
			m.showDone = !m.showDone
			m.reload()
		case "r":
			if !m.syncing {
				m.syncing = true
				return m, m.sync()
			}
		}
	}
	m.scroll()
	return m, nil
}

func (m *Model) move(d int) {
	m.cursor = min(max(0, m.cursor+d), max(0, len(m.rows)-1))
}

func (m Model) listHeight() int {
	// header + blank + footer/status + detail pane
	return max(3, m.h-4-detailLines)
}

func (m *Model) scroll() {
	h := m.listHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
}

const detailLines = 5

func (m Model) View() string {
	if m.w == 0 {
		return ""
	}
	var b strings.Builder

	// header: calendar tabs
	tabs := []string{"All"}
	for _, c := range m.cals {
		tabs = append(tabs, c.Name)
	}
	var hdr []string
	for i, t := range tabs {
		if i-1 == m.calIdx {
			hdr = append(hdr, title.Render("["+t+"]"))
		} else {
			hdr = append(hdr, dim.Render(t))
		}
	}
	b.WriteString(strings.Join(hdr, "  ") + "\n\n")

	// list
	h := m.listHeight()
	end := min(len(m.rows), m.offset+h)
	for i := m.offset; i < end; i++ {
		line := m.renderRow(m.rows[i])
		if i == m.cursor {
			line = selected.Render(padTo(stripANSI(line), m.w))
		}
		b.WriteString(line + "\n")
	}
	if len(m.rows) == 0 {
		b.WriteString(dim.Render("  No tasks.") + "\n")
		end = m.offset + 1
	}
	for i := end - m.offset; i < h; i++ {
		b.WriteString("\n")
	}

	// detail
	b.WriteString(dim.Render(strings.Repeat("─", m.w)) + "\n")
	b.WriteString(m.renderDetail())

	// status
	status := fmt.Sprintf("%d tasks", len(m.rows))
	switch {
	case m.syncing:
		status += " · syncing…"
	case m.lastSync != "":
		if t, err := time.Parse(time.RFC3339, m.lastSync); err == nil {
			status += " · synced " + t.Format("15:04")
		}
	}
	if m.showDone {
		status += " · showing completed"
	}
	keys := "j/k move · tab calendar · c completed · r refresh · q quit"
	b.WriteString(dim.Render(status+"  |  "+keys) + "\n")
	if m.err != nil {
		b.WriteString(errStyle.Render("error: " + m.err.Error()))
	}
	return b.String()
}

func (m Model) renderRow(r row) string {
	t := r.task
	box := "[ ]"
	if t.Done() {
		box = "[x]"
	}
	prio := "  "
	switch {
	case t.Priority >= 1 && t.Priority <= 4:
		prio = prioHigh.Render("!!")
	case t.Priority == 5:
		prio = prioMed.Render("! ")
	case t.Priority >= 6:
		prio = prioLow.Render("↓ ")
	}
	due := dueLabel(t)
	dueW := 12
	dueCol := padTo(due, dueW)
	switch {
	case t.Done() || t.Due.IsZero():
		dueCol = dim.Render(dueCol)
	case isOverdue(t):
		dueCol = overdue.Render(dueCol)
	case sameDay(t.Due, time.Now()):
		dueCol = today.Render(dueCol)
	}
	indent := strings.Repeat("  ", r.depth)
	sum := t.Summary
	if t.RRule != "" {
		sum += " ↻"
	}
	room := m.w - dueW - 2 - 3 - 1 - 2 - 1 - len(indent)
	sum = truncate(sum, max(10, room))
	if t.Done() {
		sum = dim.Render(sum)
	}
	return fmt.Sprintf(" %s%s %s %s %s", indent, box, prio, padTo(sum, max(10, room)), dueCol)
}

func (m Model) renderDetail() string {
	lines := make([]string, detailLines)
	if m.cursor < len(m.rows) {
		t := m.rows[m.cursor].task
		lines[0] = bold.Render(truncate(t.Summary, m.w))
		var meta []string
		if !t.Due.IsZero() {
			meta = append(meta, "due "+longDue(t))
		}
		if !t.Start.IsZero() {
			meta = append(meta, "start "+t.Start.Format("Mon Jan 2"))
		}
		if t.RRule != "" {
			meta = append(meta, "repeats "+t.RRule)
		}
		meta = append(meta, strings.ToLower(t.Status))
		lines[1] = dim.Render(strings.Join(meta, " · "))
		notes := strings.Split(strings.TrimSpace(t.Notes), "\n")
		for i := 0; i < detailLines-2 && i < len(notes); i++ {
			lines[2+i] = truncate(notes[i], m.w)
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// --- helpers ---

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Local().Date()
	by, bm, bd := b.Local().Date()
	return ay == by && am == bm && ad == bd
}

func isOverdue(t caldav.Task) bool {
	if t.Due.IsZero() {
		return false
	}
	if t.DueAllDay {
		y, mo, d := time.Now().Date()
		return t.Due.Before(time.Date(y, mo, d, 0, 0, 0, 0, time.Local))
	}
	return t.Due.Before(time.Now())
}

func dueLabel(t caldav.Task) string {
	if t.Due.IsZero() {
		return ""
	}
	now := time.Now()
	days := int(startOfDay(t.Due).Sub(startOfDay(now)).Hours() / 24)
	var d string
	switch {
	case days == 0:
		d = "today"
	case days == 1:
		d = "tomorrow"
	case days == -1:
		d = "yesterday"
	case days > 1 && days < 7:
		d = t.Due.Format("Mon")
	case t.Due.Year() == now.Year():
		d = t.Due.Format("Jan 2")
	default:
		d = t.Due.Format("2006-01-02")
	}
	if !t.DueAllDay {
		d += " " + t.Due.Format("15:04")
	}
	return d
}

func longDue(t caldav.Task) string {
	if t.DueAllDay {
		return t.Due.Format("Mon Jan 2, 2006")
	}
	return t.Due.Format("Mon Jan 2, 2006 15:04")
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Local().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:max(0, n-1)]) + "…"
}

func padTo(s string, n int) string {
	if w := lipgloss.Width(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}

func stripANSI(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			in = true
		case in && r == 'm':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}
