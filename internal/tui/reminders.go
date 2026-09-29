package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/chernesk/caldav-tui/internal/caldav"
	"github.com/chernesk/caldav-tui/internal/notify"
)

// Notifier is the desktop-notification backend (notify.Notifier in production).
type Notifier interface {
	Send(title, body string, actions []notify.Action) (uint32, error)
	Close(id uint32)
	Events() <-chan notify.Event
}

// ReminderConfig tunes in-app reminders.
type ReminderConfig struct {
	DefaultClock time.Duration // time of day all-day tasks remind
	SyncEvery    time.Duration // background sync interval while the TUI is open
	Snooze       time.Duration
	Lookback     time.Duration // how late a missed reminder may still fire
}

const (
	reminderTick = 30 * time.Second
	actComplete  = "complete"
	actSnooze    = "snooze"
)

type (
	reminderTickMsg struct{}
	syncTickMsg     struct{}
	notifyEventMsg  notify.Event
)

// WithReminders turns on desktop reminders. Reminders only fire while the TUI is running.
func (m Model) WithReminders(n Notifier, c ReminderConfig) Model {
	if c.Lookback == 0 {
		c.Lookback = 12 * time.Hour
	}
	m.notifier, m.rem = n, c
	m.notifIDs = map[uint32]string{}
	return m
}

// fireTime is when a task's reminder is due: its due time, or the default time
// of day on its due date for all-day tasks. Zero means no reminder.
func fireTime(t caldav.Task, clock time.Duration) time.Time {
	if t.Due.IsZero() {
		return time.Time{}
	}
	if !t.DueAllDay {
		return t.Due
	}
	y, mo, d := t.Due.Date()
	return time.Date(y, mo, d, 0, 0, 0, 0, time.Local).Add(clock)
}

type reminder struct {
	task    caldav.Task
	key     string
	snoozed bool
}

// dueReminders picks the reminders to show now: reminders whose time has passed
// (within lookback) and haven't been shown, plus snoozes that have woken up.
// The key includes the fire time, so a recurring task that advances re-arms.
func dueReminders(tasks []caldav.Task, now time.Time, c ReminderConfig, notified func(string) bool, snoozes map[string]time.Time) []reminder {
	var out []reminder
	for _, t := range tasks {
		if t.Done() || t.UID == "" || notStarted(t) {
			continue
		}
		if until, ok := snoozes[t.UID]; ok {
			if !until.After(now) {
				out = append(out, reminder{t, fmt.Sprintf("snooze:%s@%d", t.UID, until.Unix()), true})
			}
			continue // while snoozed, the original reminder stays quiet
		}
		ft := fireTime(t, c.DefaultClock)
		if ft.IsZero() || ft.After(now) || ft.Before(now.Add(-c.Lookback)) {
			continue
		}
		key := fmt.Sprintf("%s@%d", t.UID, ft.Unix())
		if !notified(key) {
			out = append(out, reminder{t, key, false})
		}
	}
	return out
}

func (m Model) tickReminders() tea.Cmd {
	return tea.Tick(reminderTick, func(time.Time) tea.Msg { return reminderTickMsg{} })
}

func (m Model) tickSync() tea.Cmd {
	return tea.Tick(m.rem.SyncEvery, func(time.Time) tea.Msg { return syncTickMsg{} })
}

// waitNotify blocks for the next click/dismissal and delivers it as a message.
func (m Model) waitNotify() tea.Cmd {
	ch := m.notifier.Events()
	return func() tea.Msg { return notifyEventMsg(<-ch) }
}

// checkReminders shows any due reminders. Sending is a quick local D-Bus call.
func (m *Model) checkReminders(now time.Time) {
	if m.notifier == nil {
		return
	}
	tasks, err := m.st.Tasks("")
	if err != nil {
		return
	}
	for _, r := range dueReminders(tasks, now, m.rem, m.st.WasNotified, m.st.Snoozes()) {
		title := "Task due"
		switch {
		case r.snoozed:
			title = "Task reminder"
		case r.task.DueAllDay:
			title = "Task due today"
		default:
			title = "Task due " + r.task.Due.Format("15:04")
		}
		id, err := m.notifier.Send(title, r.task.Summary, []notify.Action{
			{Key: actComplete, Label: "Complete"},
			{Key: actSnooze, Label: "Postpone " + humanDur(m.rem.Snooze)},
		})
		if err != nil {
			m.err = fmt.Errorf("notification: %w", err)
			return // try again next tick
		}
		m.notifIDs[id] = r.task.Href
		_ = m.st.MarkNotified(r.key, now)
		if r.snoozed {
			_ = m.st.ClearSnooze(r.task.UID)
		}
	}
}

// handleNotify reacts to a click on a notification button.
func (m Model) handleNotify(ev notify.Event) (Model, tea.Cmd) {
	href, known := m.notifIDs[ev.ID]
	if ev.Closed || ev.Action == "" {
		delete(m.notifIDs, ev.ID)
		return m, m.waitNotify()
	}
	if !known {
		return m, m.waitNotify()
	}
	var task caldav.Task
	found := false
	if all, err := m.st.Tasks(""); err == nil {
		for _, t := range all {
			if t.Href == href {
				task, found = t, true
			}
		}
	}
	if !found || task.Done() {
		m.notifier.Close(ev.ID)
		return m, m.waitNotify()
	}
	switch ev.Action {
	case actComplete:
		m.notifier.Close(ev.ID)
		m.busy++
		return m, tea.Batch(m.toggleDone(task), m.waitNotify())
	case actSnooze:
		m.notifier.Close(ev.ID)
		_ = m.st.Snooze(task.UID, time.Now().Add(m.rem.Snooze))
		m.flash = "postponed " + humanDur(m.rem.Snooze) + ": " + truncate(task.Summary, 40)
	}
	return m, m.waitNotify()
}

func humanDur(d time.Duration) string {
	if d >= time.Hour && d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	if d >= time.Hour {
		return fmt.Sprintf("%.1fh", d.Hours())
	}
	return fmt.Sprintf("%dm", int(d/time.Minute))
}
