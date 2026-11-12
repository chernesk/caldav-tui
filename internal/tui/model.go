// Package tui is the Bubble Tea front end. It only ever reads the local cache;
// sync runs in the background and the list reloads when it finishes.
package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/chernesk/caldav-tui/internal/caldav"
	"github.com/chernesk/caldav-tui/internal/notify"
	"github.com/chernesk/caldav-tui/internal/store"
	"github.com/chernesk/caldav-tui/internal/version"
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
	task      caldav.Task
	depth     int
	prefix    string // tree guide, e.g. "│ ├ "
	collapsed bool   // has subtasks that are folded away
}

// progress counts a parent's direct subtasks, including completed or hidden ones.
type progress struct{ done, total int }

type mode int

const (
	modeList mode = iota
	modeForm
	modeQuick
	modeConfirmDelete
)

type Model struct {
	mode        mode
	form        *form
	quick       *quickBox
	confirm     caldav.Task         // task pending delete confirmation
	flash       string              // one-line result of the last action, cleared on next key
	busy        int                 // writes in flight
	defaultPrio int                 // priority given to new tasks that don't specify one (0 = none)
	collapsed   map[string]bool     // parent UID -> subtasks folded away (persisted)
	progress    map[string]progress // parent UID -> direct subtask counts
	pending     map[string]bool     // href -> change applied locally, not yet confirmed by the server

	notifier Notifier // nil = reminders off
	rem      ReminderConfig
	notifIDs map[uint32]string // open notification id -> task href

	cl *caldav.Client
	st *store.Store

	cals       []caldav.Calendar
	calIdx     int // -1 = all calendars
	rows       []row
	cursor     int
	offset     int
	showDone   bool
	showFuture bool // include tasks whose start date is after today
	hidden     int  // tasks filtered out as not yet started

	syncing  bool
	lastSync string
	err      error

	w, h int
}

func New(cl *caldav.Client, st *store.Store) Model {
	m := Model{cl: cl, st: st, calIdx: -1, syncing: true, pending: map[string]bool{}, collapsed: st.Collapsed(), progress: map[string]progress{}}
	m.reload()
	return m
}

type syncDone struct{ err error }

func (m Model) sync() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_, err := Sync(ctx, m.cl, m.st)
		return syncDone{err}
	}
}

// WithDefaultPriority sets the priority (0 = none, 1 high .. 9 low) given to new
// tasks that don't specify one. Existing tasks are never changed.
func (m Model) WithDefaultPriority(p int) Model {
	m.defaultPrio = p
	return m
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.sync()}
	if m.notifier != nil {
		cmds = append(cmds, m.tickReminders(), m.tickSync(), m.waitNotify())
	}
	return tea.Batch(cmds...)
}

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
	m.progress = map[string]progress{}
	for _, t := range tasks {
		if t.Parent != "" && t.Parent != t.UID {
			p := m.progress[t.Parent]
			p.total++
			if t.Done() {
				p.done++
			}
			m.progress[t.Parent] = p
		}
	}
	var keep []caldav.Task
	m.hidden = 0
	for _, t := range tasks {
		if t.Done() && !m.showDone {
			continue
		}
		if !m.showFuture && notStarted(t) {
			m.hidden++
			continue
		}
		keep = append(keep, t)
	}
	m.rows = buildTree(keep, m.collapsed)
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

// prioRank orders priorities (1 = highest); no priority sorts last.
func prioRank(p int) int {
	if p == 0 {
		return 10
	}
	return p
}

// buildTree orders tasks with subtasks nested under their RELATED-TO parent.
// A task whose parent isn't in the visible set is shown as a root.
func buildTree(tasks []caldav.Task, collapsed map[string]bool) []row {
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
	// prefix is this row's guide (├ / └), childPrefix the guide stem for its children
	var walk func(t caldav.Task, depth int, prefix, childPrefix string)
	walk = func(t caldav.Task, depth int, prefix, childPrefix string) {
		if seen[t.UID] {
			return // guards against parent cycles
		}
		seen[t.UID] = true
		c := kids[t.UID]
		fold := collapsed[t.UID] && len(c) > 0
		out = append(out, row{task: t, depth: depth, prefix: prefix, collapsed: fold})
		if fold {
			return
		}
		sort.SliceStable(c, func(i, j int) bool { return less(c[i], c[j]) })
		for i, k := range c {
			if i == len(c)-1 {
				walk(k, depth+1, childPrefix+"└ ", childPrefix+"  ")
			} else {
				walk(k, depth+1, childPrefix+"├ ", childPrefix+"│ ")
			}
		}
	}
	for _, r := range roots {
		walk(r, 0, "", "")
	}
	return out
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		if m.form != nil {
			m.form.resize(m.w)
		}
		if m.quick != nil {
			m.quick.input.Width = max(20, min(m.w-10, 90))
		}
		m.scroll()
		return m, nil
	case syncDone:
		m.syncing = false
		m.err = msg.err
		m.reload()
		m.scroll()
		m.checkReminders(time.Now()) // fresh data: catch anything that just became due
		return m, nil
	case reminderTickMsg:
		m.checkReminders(time.Now())
		return m, m.tickReminders()
	case syncTickMsg:
		if m.syncing {
			return m, m.tickSync()
		}
		m.syncing = true
		return m, tea.Batch(m.sync(), m.tickSync())
	case notifyEventMsg:
		return m.handleNotify(notify.Event(msg))
	case opMsg:
		return m.opDone(msg)
	}

	switch m.mode {
	case modeForm:
		return m.updateForm(msg)
	case modeQuick:
		return m.updateQuick(msg)
	case modeConfirmDelete:
		if km, ok := msg.(tea.KeyMsg); ok {
			m.mode = modeList
			if km.String() == "y" || km.String() == "Y" {
				return m.startDelete(m.confirm)
			}
		}
		return m, nil
	}

	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	m.flash = ""
	switch km.String() {
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
	case "s":
		m.showFuture = !m.showFuture
		m.reload()
	case "r":
		if !m.syncing {
			m.syncing = true
			return m, m.sync()
		}
	case "z":
		if t, ok := m.current(); ok && m.progress[t.UID].total > 0 {
			m.setFold(t.UID, !m.collapsed[t.UID])
		}
	case "h", "left":
		m.fold(true)
	case "l", "right":
		m.fold(false)
	case "x", " ":
		if t, ok := m.current(); ok {
			return m.startToggle(t)
		}
	case "a":
		if cal := m.targetCalendar(); cal != "" {
			m.quick = newQuickBox("", "", cal, m.w, m.defaultPrio)
			m.mode = modeQuick
			return m, textinput.Blink
		}
		m.err = errors.New("no task calendar yet; sync first")
	case "A":
		if t, ok := m.current(); ok {
			m.quick = newQuickBox(t.UID, t.Summary, t.Calendar, m.w, m.defaultPrio)
			m.mode = modeQuick
			return m, textinput.Blink
		}
	case "e", "enter":
		if t, ok := m.current(); ok {
			m.form = newForm(&t, "", "", t.Calendar, m.w, 0)
			m.mode = modeForm
			return m, textinput.Blink
		}
	case "d":
		if t, ok := m.current(); ok {
			m.confirm = t
			m.mode = modeConfirmDelete
		}
	}
	m.scroll()
	return m, nil
}

func (m Model) current() (caldav.Task, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return caldav.Task{}, false
	}
	return m.rows[m.cursor].task, true
}

// targetCalendar picks where a new task goes: the selected tab, else the
// selected task's calendar, else the first task calendar.
func (m Model) targetCalendar() string {
	if m.calIdx >= 0 && m.calIdx < len(m.cals) {
		return m.cals[m.calIdx].Path
	}
	if t, ok := m.current(); ok {
		return t.Calendar
	}
	if len(m.cals) > 0 {
		return m.cals[0].Path
	}
	return ""
}

func (m Model) updateQuick(msg tea.Msg) (tea.Model, tea.Cmd) {
	act, cmd := m.quick.update(msg)
	switch act {
	case quickCancel:
		m.mode, m.quick = modeList, nil
	case quickExpand:
		m.form = m.quick.expandForm(time.Now(), m.w)
		m.mode, m.quick = modeForm, nil
		return m, textinput.Blink
	case quickSubmit:
		p, err := QuickParse(m.quick.input.Value(), time.Now())
		if err != nil {
			return m, cmd // the prompt's preview line already shows why; stay open
		}
		q := m.quick
		m.mode, m.quick = modeList, nil
		return m.startSave(&form{parent: q.parent, calendar: q.calendar}, p.WithDefault(m.defaultPrio).Edit())
	}
	return m, cmd
}

func (m Model) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	act, cmd := m.form.update(msg)
	switch act {
	case formCancel:
		m.mode, m.form = modeList, nil
	case formSubmit:
		e, err := m.form.edit(time.Now())
		if err != nil {
			m.form.err = err.Error()
			return m, cmd
		}
		f := m.form
		m.mode, m.form = modeList, nil
		if f.task != nil && e == (caldav.Edit{}) {
			return m, nil // nothing changed
		}
		return m.startSave(f, e)
	}
	return m, cmd
}

// fold collapses (h) or expands (l) the selected parent. Like a file tree, h on
// something that can't fold further jumps up to its parent.
func (m *Model) fold(collapse bool) {
	t, ok := m.current()
	if !ok {
		return
	}
	hasKids := m.progress[t.UID].total > 0
	switch {
	case collapse && hasKids && !m.collapsed[t.UID]:
		m.setFold(t.UID, true)
	case collapse && t.Parent != "":
		m.selectUID(t.Parent)
	case !collapse && hasKids && m.collapsed[t.UID]:
		m.setFold(t.UID, false)
	}
}

func (m *Model) setFold(uid string, on bool) {
	if on {
		m.collapsed[uid] = true
	} else {
		delete(m.collapsed, uid)
	}
	_ = m.st.SetCollapsed(uid, on)
	m.reload()
	m.selectUID(uid)
}

// selectUID moves the cursor to a task if it's visible.
func (m *Model) selectUID(uid string) {
	for i, r := range m.rows {
		if r.task.UID == uid {
			m.cursor = i
			return
		}
	}
}

func (m *Model) move(d int) {
	m.cursor = min(max(0, m.cursor+d), max(0, len(m.rows)-1))
}

func (m Model) listHeight() int {
	// header (2) + separator + status + error line + detail pane
	return max(3, m.h-5-detailLines)
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
	if m.mode == modeForm && m.form != nil {
		return m.form.view(time.Now())
	}
	if m.mode == modeQuick && m.quick != nil {
		return m.quick.view(time.Now())
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
	if m.showFuture {
		status += " · showing unstarted"
	} else if m.hidden > 0 {
		status += fmt.Sprintf(" · %d unstarted hidden", m.hidden)
	}
	if m.busy > 0 {
		status += " · saving…"
	}
	keys := "x done · a add · e edit · d del · s unstarted · q quit · v" + version.String()
	switch {
	case m.mode == modeConfirmDelete:
		q := "Delete “" + m.confirm.Summary + "”? (y/N)"
		if n := m.progress[m.confirm.UID].total; n > 0 {
			q = fmt.Sprintf("Delete “%s”? Its %d subtask(s) are NOT deleted; they stay as separate tasks. (y/N)", truncate(m.confirm.Summary, 30), n)
		}
		b.WriteString(errStyle.Render(truncate(q, m.w)))
	case m.flash != "":
		b.WriteString(today.Render(truncate(m.flash, m.w)))
	default:
		b.WriteString(dim.Render(truncate(status+"  |  "+keys, m.w)))
	}
	// Always reserve the error line, and end without a newline, so the view is
	// exactly m.h lines and the header never scrolls off the top.
	b.WriteString("\n")
	if m.err != nil {
		b.WriteString(errStyle.Render(truncate("error: "+m.err.Error(), m.w)))
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
	case t.Priority >= 6 && t.Priority != m.defaultPrio:
		prio = prioLow.Render("↓ ") // the default level is the baseline: no marker
	case t.Priority == 0 && m.defaultPrio != 0:
		prio = dim.Render("– ") // with a default set, "no priority" is the odd one out
	}
	due := dueLabel(t)
	dueW := 14
	dueCol := padTo(due, dueW)
	switch {
	case t.Done() || t.Due.IsZero():
		dueCol = dim.Render(dueCol)
	case isOverdue(t):
		dueCol = overdue.Render(dueCol)
	case sameDay(t.Due, time.Now()):
		dueCol = today.Render(dueCol)
	}
	indent := dim.Render(r.prefix)
	sum := t.Summary
	if t.RRule != "" {
		sum += " ↻"
	}
	if p := m.progress[t.UID]; p.total > 0 {
		sum += fmt.Sprintf(" (%d/%d)", p.done, p.total)
		if r.collapsed {
			sum += " ▸"
		}
	}
	if m.pending[t.Href] {
		sum += " ⟳"
	}
	room := m.w - dueW - 2 - 3 - 1 - 2 - 1 - lipgloss.Width(indent)
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
		if p := m.progress[t.UID]; p.total > 0 {
			meta = append(meta, fmt.Sprintf("%d of %d subtasks done", p.done, p.total))
		}
		switch {
		case t.Priority != 0:
			meta = append(meta, PriorityText(t.Priority)+" priority")
		case m.defaultPrio != 0:
			meta = append(meta, "no priority")
		}
		meta = append(meta, strings.ToLower(t.Status))
		if al := t.Alarms(); len(al) > 0 {
			meta = append(meta, "alarm "+strings.Join(al, ", "))
		}
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

// notStarted reports whether the task's start date is after today. Like the
// Chrome extension, this compares calendar days, so a task starting later today
// is already visible.
func notStarted(t caldav.Task) bool {
	return !t.Start.IsZero() && startOfDay(t.Start).After(startOfDay(time.Now()))
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
