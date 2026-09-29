package tui

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/chernesk/caldav-tui/internal/caldav"
	"github.com/chernesk/caldav-tui/internal/store"
	"github.com/chernesk/caldav-tui/internal/testdav"
)

type rig struct {
	t   *testing.T
	m   Model
	srv *testdav.Server
	st  *store.Store
}

const calPath = "/dav/cal/"

func newRig(t *testing.T) *rig {
	f := testdav.New()
	ts := httptest.NewServer(f)
	t.Cleanup(ts.Close)
	cl, err := caldav.New(ts.URL+"/dav/", "u", "p")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.ReplaceCalendar(caldav.Calendar{Path: calPath, Name: "Tasks"}, nil); err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, srv: f, st: st}
	r.m = New(cl, st)
	r.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	return r
}

// seed puts a task on the server and in the cache, as a prior sync would have.
func (r *rig) seed(uid string, e caldav.Edit) {
	raw, err := caldav.NewRaw(uid, "", e, time.Now())
	if err != nil {
		r.t.Fatal(err)
	}
	href := calPath + uid + ".ics"
	etag, err := r.m.cl.Put(context.Background(), href, raw, "")
	if err != nil {
		r.t.Fatal(err)
	}
	task, err := caldav.TaskFromRaw(href, etag, calPath, raw)
	if err != nil {
		r.t.Fatal(err)
	}
	if err := r.st.UpsertTask(task); err != nil {
		r.t.Fatal(err)
	}
	r.m.reload()
}

// send delivers a message, then runs any resulting command that finishes
// quickly (skipping cursor-blink timers) and feeds its result back.
func (r *rig) send(msg tea.Msg) {
	r.t.Helper()
	next, cmd := r.m.Update(msg)
	r.m = next.(Model)
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case res := <-done:
		switch res.(type) {
		case opMsg, syncDone:
			r.send(res)
		}
	case <-time.After(300 * time.Millisecond):
	}
}

func (r *rig) key(s string) {
	r.t.Helper()
	switch s {
	case "enter":
		r.send(tea.KeyMsg{Type: tea.KeyEnter})
	case "tab":
		r.send(tea.KeyMsg{Type: tea.KeyTab})
	case "ctrl+s":
		r.send(tea.KeyMsg{Type: tea.KeyCtrlS})
	case "esc":
		r.send(tea.KeyMsg{Type: tea.KeyEsc})
	default:
		r.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	}
}

func (r *rig) server(uid string) string {
	return r.srv.Get(calPath + uid + ".ics")
}

func (r *rig) cached(uid string) (caldav.Task, bool) {
	ts, _ := r.st.Tasks("")
	for _, t := range ts {
		if t.UID == uid {
			return t, true
		}
	}
	return caldav.Task{}, false
}

func (r *rig) cursorOn(uid string) {
	r.t.Helper()
	for i, row := range r.m.rows {
		if row.task.UID == uid {
			r.m.cursor = i
			return
		}
	}
	r.t.Fatalf("%s not visible", uid)
}

func day(offset int) time.Time {
	n := time.Now()
	return time.Date(n.Year(), n.Month(), n.Day()+offset, 0, 0, 0, 0, time.UTC)
}

func TestFlowCompleteAndReopen(t *testing.T) {
	r := newRig(t)
	sum := "Plain"
	r.seed("plain", caldav.Edit{Summary: &sum})
	r.cursorOn("plain")

	r.key("x")
	if !strings.Contains(r.server("plain"), "STATUS:COMPLETED") {
		t.Fatalf("server not completed:\n%s", r.server("plain"))
	}
	if c, _ := r.cached("plain"); !c.Done() {
		t.Error("cache not completed")
	}
	if len(r.m.rows) != 0 {
		t.Errorf("completed task should be hidden, rows=%d", len(r.m.rows))
	}
	if r.m.err != nil {
		t.Error(r.m.err)
	}

	r.key("c") // show completed
	r.cursorOn("plain")
	r.key("x") // reopen
	if !strings.Contains(r.server("plain"), "STATUS:NEEDS-ACTION") || strings.Contains(r.server("plain"), "COMPLETED:") {
		t.Errorf("not reopened:\n%s", r.server("plain"))
	}
}

func TestFlowCompleteRecurringAdvances(t *testing.T) {
	r := newRig(t)
	sum, rr := "Weekly", "FREQ=WEEKLY"
	due := caldav.When{T: day(0)}
	r.seed("rec", caldav.Edit{Summary: &sum, RRule: &rr, Due: &due})
	r.cursorOn("rec")

	r.key("x")
	c, _ := r.cached("rec")
	if c.Done() {
		t.Fatal("recurring task must stay open")
	}
	if got, want := c.Due.Format("2006-01-02"), day(7).Format("2006-01-02"); got != want {
		t.Errorf("due = %s, want %s", got, want)
	}
	if !strings.Contains(r.m.flash, "next due") {
		t.Errorf("flash = %q", r.m.flash)
	}
}

func TestFlowAddEditDelete(t *testing.T) {
	r := newRig(t)

	// add
	r.key("a")
	if r.m.mode != modeForm {
		t.Fatal("form did not open")
	}
	r.key("Buy milk")
	r.key("tab")
	r.key("tomorrow 2pm")
	r.key("tab")
	r.key("tab")
	r.key("high")
	r.key("enter")
	if r.m.mode != modeList || r.m.err != nil {
		t.Fatalf("mode=%v err=%v form err=%v", r.m.mode, r.m.err, r.m.form)
	}
	if len(r.m.rows) != 1 {
		t.Fatalf("rows=%d", len(r.m.rows))
	}
	tk := r.m.rows[0].task
	if tk.Summary != "Buy milk" || tk.Priority != 1 || tk.DueAllDay || tk.Due.Hour() != 14 {
		t.Errorf("bad task: %+v", tk)
	}
	raw := r.server(tk.UID)
	for _, want := range []string{"SUMMARY:Buy milk", "PRIORITY:1", "DUE:" + day(1).Format("20060102") + "T140000"} {
		if !strings.Contains(raw, want) {
			t.Errorf("server missing %q:\n%s", want, raw)
		}
	}

	// edit: rename only; due must be left byte-for-byte alone
	r.cursorOn(tk.UID)
	r.key("e")
	if r.m.form == nil || r.m.form.task == nil {
		t.Fatal("edit form did not open")
	}
	r.key(" now") // appends to the focused title
	r.key("ctrl+s")
	raw = r.server(tk.UID)
	if !strings.Contains(raw, "SUMMARY:Buy milk now") || !strings.Contains(raw, "PRIORITY:1") {
		t.Errorf("edit failed:\n%s", raw)
	}

	// edit with no changes writes nothing
	gen := r.srv.Gen(calPath + tk.UID + ".ics")
	r.cursorOn(tk.UID)
	r.key("e")
	r.key("ctrl+s")
	if r.srv.Gen(calPath+tk.UID+".ics") != gen {
		t.Error("no-op edit hit the server")
	}

	// invalid input keeps the form open with an error
	r.cursorOn(tk.UID)
	r.key("e")
	r.key("tab")
	r.key("blorp")
	r.key("enter")
	if r.m.mode != modeForm || r.m.form.err == "" {
		t.Errorf("bad date should keep form open: mode=%v", r.m.mode)
	}
	r.key("esc")
	if r.m.mode != modeList {
		t.Error("esc should close form")
	}

	// delete: n cancels, y deletes
	r.cursorOn(tk.UID)
	r.key("d")
	r.key("n")
	if r.server(tk.UID) == "" {
		t.Fatal("n should not delete")
	}
	r.key("d")
	r.key("y")
	if r.server(tk.UID) != "" {
		t.Error("server still has task")
	}
	if _, ok := r.cached(tk.UID); ok {
		t.Error("cache still has task")
	}
}

func TestFlowSubtask(t *testing.T) {
	r := newRig(t)
	sum := "Parent"
	r.seed("par", caldav.Edit{Summary: &sum})
	r.cursorOn("par")
	r.key("A")
	r.key("Child")
	r.key("enter")
	if len(r.m.rows) != 2 || r.m.rows[1].depth != 1 || r.m.rows[1].task.Summary != "Child" {
		t.Fatalf("subtask not nested under parent: %+v", r.m.rows)
	}
	if !strings.Contains(r.server(r.m.rows[1].task.UID), "RELATED-TO:par") {
		t.Error("RELATED-TO missing")
	}
}

func TestFlowConflictTriggersSync(t *testing.T) {
	r := newRig(t)
	sum := "Racy"
	r.seed("racy", caldav.Edit{Summary: &sum})
	r.cursorOn("racy")
	r.m.syncing = false // New() starts mid-sync; pretend the startup sync finished

	// someone else changes it on the server, bumping the ETag
	r.srv.Bump(calPath + "racy.ics")

	next, cmd := r.m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	r.m = next.(Model)
	res := cmd()
	om, ok := res.(opMsg)
	if !ok || !om.conflict {
		t.Fatalf("want conflict opMsg, got %#v", res)
	}
	if strings.Contains(r.server("racy"), "COMPLETED") {
		t.Error("stale write went through")
	}
	next, cmd = r.m.Update(om)
	r.m = next.(Model)
	if cmd == nil || !r.m.syncing || r.m.err == nil {
		t.Errorf("conflict should start a sync and show an error: syncing=%v err=%v", r.m.syncing, r.m.err)
	}
}
