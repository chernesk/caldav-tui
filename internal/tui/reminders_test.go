package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/chernesk/caldav-tui/internal/caldav"
	"github.com/chernesk/caldav-tui/internal/notify"
)

var rc = ReminderConfig{DefaultClock: 9 * time.Hour, Snooze: time.Hour, Lookback: 12 * time.Hour}

func at(h, m int) time.Time {
	n := time.Now()
	return time.Date(n.Year(), n.Month(), n.Day(), h, m, 0, 0, time.Local)
}

func never(string) bool { return false }

func TestDueReminders(t *testing.T) {
	allDay := caldav.Task{UID: "a", Status: "NEEDS-ACTION", Due: at(0, 0), DueAllDay: true}
	timed := caldav.Task{UID: "t", Status: "NEEDS-ACTION", Due: at(14, 30)}
	done := caldav.Task{UID: "d", Status: "COMPLETED", Due: at(8, 0)}
	future := caldav.Task{UID: "f", Status: "NEEDS-ACTION", Due: at(0, 0).AddDate(0, 0, 1), DueAllDay: true}
	noDue := caldav.Task{UID: "n", Status: "NEEDS-ACTION"}
	unstarted := caldav.Task{UID: "u", Status: "NEEDS-ACTION", Due: at(8, 0), Start: at(0, 0).AddDate(0, 0, 2)}
	old := caldav.Task{UID: "o", Status: "NEEDS-ACTION", Due: at(0, 0).AddDate(0, 0, -3), DueAllDay: true}
	all := []caldav.Task{allDay, timed, done, future, noDue, unstarted, old}

	ids := func(rs []reminder) string {
		var s []string
		for _, r := range rs {
			s = append(s, r.task.UID)
		}
		return strings.Join(s, ",")
	}
	// 08:59: nothing yet (all-day fires at 09:00)
	if got := ids(dueReminders(all, at(8, 59), rc, never, nil)); got != "" {
		t.Errorf("08:59 got %q", got)
	}
	// 09:00: the all-day task only
	if got := ids(dueReminders(all, at(9, 0), rc, never, nil)); got != "a" {
		t.Errorf("09:00 got %q", got)
	}
	// 14:30: timed joins; done/future/no-due/unstarted/too-old never fire
	if got := ids(dueReminders(all, at(14, 30), rc, never, nil)); got != "a,t" {
		t.Errorf("14:30 got %q", got)
	}
	// already notified -> silent
	rs := dueReminders(all, at(14, 30), rc, never, nil)
	seen := map[string]bool{}
	for _, r := range rs {
		seen[r.key] = true
	}
	if got := ids(dueReminders(all, at(14, 31), rc, func(k string) bool { return seen[k] }, nil)); got != "" {
		t.Errorf("duplicate fired: %q", got)
	}
	// a recurring task that advances gets a new key, so it re-arms
	adv := timed
	adv.Due = timed.Due.AddDate(0, 0, 7)
	k1 := dueReminders([]caldav.Task{timed}, at(14, 30), rc, never, nil)[0].key
	k2 := dueReminders([]caldav.Task{adv}, adv.Due, rc, never, nil)[0].key
	if k1 == k2 {
		t.Error("advanced task reuses the old key")
	}
}

func TestSnoozedReminders(t *testing.T) {
	tk := caldav.Task{UID: "s", Status: "NEEDS-ACTION", Due: at(9, 0)}
	sn := map[string]time.Time{"s": at(11, 0)}
	// still snoozed: silent, even though the original time passed and was never marked
	if got := dueReminders([]caldav.Task{tk}, at(10, 0), rc, never, sn); len(got) != 0 {
		t.Errorf("fired while snoozed: %v", got)
	}
	got := dueReminders([]caldav.Task{tk}, at(11, 0), rc, never, sn)
	if len(got) != 1 || !got[0].snoozed {
		t.Errorf("snooze did not wake: %v", got)
	}
}

// --- through the model, with a fake desktop ---

type sent struct {
	title, body string
	actions     []notify.Action
}

type fakeNotifier struct {
	sent   []sent
	closed []uint32
	ev     chan notify.Event
}

func newFakeNotifier() *fakeNotifier { return &fakeNotifier{ev: make(chan notify.Event, 4)} }

func (f *fakeNotifier) Send(title, body string, a []notify.Action) (uint32, error) {
	f.sent = append(f.sent, sent{title, body, a})
	return uint32(len(f.sent)), nil
}
func (f *fakeNotifier) Close(id uint32)             { f.closed = append(f.closed, id) }
func (f *fakeNotifier) Events() <-chan notify.Event { return f.ev }

func TestReminderFlow(t *testing.T) {
	r := newRig(t)
	fn := newFakeNotifier()
	r.m = r.m.WithReminders(fn, rc)

	sum := "Call dentist"
	due := caldav.When{T: time.Now().Add(-time.Minute).Truncate(time.Second), HasTime: true}
	r.seed("dent", caldav.Edit{Summary: &sum, Due: &due})

	// fires once, then not again on the next tick
	r.send(reminderTickMsg{})
	r.send(reminderTickMsg{})
	if len(fn.sent) != 1 {
		t.Fatalf("sent %d notifications, want 1", len(fn.sent))
	}
	if fn.sent[0].body != sum || len(fn.sent[0].actions) != 2 {
		t.Errorf("bad notification: %+v", fn.sent[0])
	}

	// a "new process" (fresh model, same cache) must not re-notify
	m2 := r.m
	m2.notifIDs = map[uint32]string{}
	m2.checkReminders(time.Now())
	if len(fn.sent) != 1 {
		t.Errorf("restart re-notified: %d", len(fn.sent))
	}

	// Postpone: closes the popup and snoozes; the snooze wakes later
	r.m, _ = r.m.handleNotify(notify.Event{ID: 1, Action: actSnooze})
	if len(fn.closed) != 1 || !strings.Contains(r.m.flash, "postponed") {
		t.Errorf("snooze: closed=%v flash=%q", fn.closed, r.m.flash)
	}
	if _, ok := r.st.Snoozes()["dent"]; !ok {
		t.Fatal("snooze not stored")
	}
	r.m.checkReminders(time.Now().Add(2 * time.Hour))
	if len(fn.sent) != 2 || fn.sent[1].title != "Task reminder" {
		t.Fatalf("snooze did not re-fire: %+v", fn.sent)
	}
	if _, ok := r.st.Snoozes()["dent"]; ok {
		t.Error("snooze not cleared after firing")
	}
}

// runBatch executes the commands in a tea.Batch (skipping ones that block, like the
// notification listener) and feeds any opMsg results back into the model.
func (r *rig) runBatch(cmd tea.Cmd) {
	r.t.Helper()
	msg := cmd()
	cmds := []tea.Cmd{func() tea.Msg { return msg }}
	if b, ok := msg.(tea.BatchMsg); ok {
		cmds = b
	}
	for _, c := range cmds {
		done := make(chan tea.Msg, 1)
		go func() { done <- c() }()
		select {
		case res := <-done:
			if om, ok := res.(opMsg); ok {
				r.send(om)
			}
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func TestCompleteButtonWritesThroughSafePath(t *testing.T) {
	r := newRig(t)
	fn := newFakeNotifier()
	r.m = r.m.WithReminders(fn, rc)
	sum := "Pay rent"
	due := caldav.When{T: time.Now().Add(-time.Minute).Truncate(time.Second), HasTime: true}
	r.seed("rent", caldav.Edit{Summary: &sum, Due: &due})
	r.send(reminderTickMsg{})
	if len(fn.sent) != 1 {
		t.Fatalf("sent %d", len(fn.sent))
	}

	next, cmd := r.m.handleNotify(notify.Event{ID: 1, Action: actComplete})
	r.m = next
	r.runBatch(cmd)
	if !strings.Contains(r.server("rent"), "STATUS:COMPLETED") {
		t.Fatalf("server not completed:\n%s", r.server("rent"))
	}
	if c, _ := r.cached("rent"); !c.Done() {
		t.Error("cache not completed")
	}
	if len(fn.closed) != 1 {
		t.Errorf("popup not closed: %v", fn.closed)
	}

	// clicking Complete on a stale notification whose task changed elsewhere must not clobber it
	r2 := newRig(t)
	fn2 := newFakeNotifier()
	r2.m = r2.m.WithReminders(fn2, rc)
	r2.seed("race", caldav.Edit{Summary: &sum, Due: &due})
	r2.send(reminderTickMsg{})
	r2.srv.Bump(calPath + "race.ics")
	next, cmd = r2.m.handleNotify(notify.Event{ID: 1, Action: actComplete})
	r2.m = next
	r2.runBatch(cmd)
	if strings.Contains(r2.server("race"), "COMPLETED") {
		t.Error("stale complete overwrote the server copy")
	}
	if r2.m.err == nil {
		t.Error("conflict should surface an error")
	}
}
