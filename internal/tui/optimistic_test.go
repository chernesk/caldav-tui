package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/chernesk/caldav-tui/internal/caldav"
)

// hold sends a key but does NOT run the resulting command, so the test can look
// at the state between "applied locally" and "server answered".
func (r *rig) hold(k string) tea.Cmd {
	r.t.Helper()
	var msg tea.KeyMsg
	switch k {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	next, cmd := r.m.Update(msg)
	r.m = next.(Model)
	return cmd
}

// finish runs a push command and delivers its result.
func (r *rig) finish(cmd tea.Cmd) opMsg {
	r.t.Helper()
	if cmd == nil {
		r.t.Fatal("no push command")
	}
	om, ok := cmd().(opMsg)
	if !ok {
		r.t.Fatal("command did not produce an opMsg")
	}
	next, _ := r.m.Update(om)
	r.m = next.(Model)
	return om
}

func TestOptimisticVisibleBeforeServerAnswers(t *testing.T) {
	r := newRig(t)
	sum := "Plain"
	r.seed("plain", caldav.Edit{Summary: &sum})
	r.cursorOn("plain")
	r.key("c") // show completed so the row stays visible
	r.cursorOn("plain")

	cmd := r.hold("x")
	c, _ := r.cached("plain")
	if !c.Done() {
		t.Error("cache should already show the task completed")
	}
	if strings.Contains(r.server("plain"), "STATUS:COMPLETED") {
		t.Error("server should not have been touched yet")
	}
	href := calPath + "plain.ics"
	if !r.m.pending[href] || !strings.Contains(r.m.View(), "⟳") {
		t.Error("task should be marked as saving")
	}
	if r.m.flash != "completed" {
		t.Errorf("flash = %q", r.m.flash)
	}

	r.finish(cmd)
	if !strings.Contains(r.server("plain"), "STATUS:COMPLETED") {
		t.Errorf("server not updated:\n%s", r.server("plain"))
	}
	if r.m.pending[href] || strings.Contains(r.m.View(), "⟳") {
		t.Error("saving marker should clear")
	}
	// the cache carries the server's new ETag, so the next write is valid
	c, _ = r.cached("plain")
	if c.ETag != `"c"` {
		t.Errorf("cached ETag = %q, want the server's new one", c.ETag)
	}
	if r.m.err != nil {
		t.Error(r.m.err)
	}
}

func TestOptimisticRollbackOnConflict(t *testing.T) {
	r := newRig(t)
	sum := "Racy"
	r.seed("racy", caldav.Edit{Summary: &sum})
	r.cursorOn("racy")
	r.m.syncing = false
	r.srv.Bump(calPath + "racy.ics") // changed elsewhere

	cmd := r.hold("x")
	if c, _ := r.cached("racy"); !c.Done() {
		t.Fatal("expected the optimistic change first")
	}
	if len(r.m.rows) != 0 {
		t.Error("completed task should vanish from the list at once")
	}

	r.finish(cmd)
	c, _ := r.cached("racy")
	if c.Done() {
		t.Error("change was not rolled back")
	}
	if len(r.m.rows) != 1 {
		t.Error("task should be back in the list")
	}
	if r.m.err == nil || !strings.Contains(r.m.err.Error(), "undone") {
		t.Errorf("err = %v", r.m.err)
	}
	if !r.m.syncing {
		t.Error("conflict should trigger a refresh")
	}
	if strings.Contains(r.server("racy"), "COMPLETED") {
		t.Error("stale write reached the server")
	}
}

func TestOptimisticAddRollsBackWhenServerDown(t *testing.T) {
	r := newRig(t)
	r.ts.Close() // network gone

	r.key("a")
	r.key("Buy milk")
	cmd := r.hold("enter")
	if len(r.m.rows) != 1 || r.m.rows[0].task.Summary != "Buy milk" {
		t.Fatalf("new task should appear immediately: %+v", r.m.rows)
	}
	if !strings.Contains(r.m.View(), "⟳") {
		t.Error("new task should show the saving marker")
	}

	r.finish(cmd)
	if len(r.m.rows) != 0 {
		t.Errorf("failed add should disappear again: %+v", r.m.rows)
	}
	if r.m.err == nil || !strings.Contains(r.m.err.Error(), "not saved") {
		t.Errorf("err = %v", r.m.err)
	}
}

func TestOptimisticEditRollsBackWhenServerDown(t *testing.T) {
	r := newRig(t)
	sum := "Original"
	r.seed("orig", caldav.Edit{Summary: &sum})
	r.cursorOn("orig")
	r.ts.Close()

	r.key("e")
	r.key(" changed")
	cmd := r.hold("enter")
	if r.m.rows[0].task.Summary != "Original changed" {
		t.Fatalf("edit should show at once, got %q", r.m.rows[0].task.Summary)
	}
	r.finish(cmd)
	if got := r.m.rows[0].task.Summary; got != "Original" {
		t.Errorf("edit not rolled back: %q", got)
	}
}

func TestOptimisticDeleteRollsBack(t *testing.T) {
	r := newRig(t)
	sum := "Keep me"
	r.seed("keep", caldav.Edit{Summary: &sum})
	r.cursorOn("keep")
	r.ts.Close()

	r.key("d")
	cmd := r.hold("y")
	if len(r.m.rows) != 0 {
		t.Error("deleted task should vanish at once")
	}
	r.finish(cmd)
	if len(r.m.rows) != 1 || r.m.rows[0].task.UID != "keep" {
		t.Errorf("failed delete should restore the task: %+v", r.m.rows)
	}
}

func TestOptimisticBlocksSecondWriteToSameTask(t *testing.T) {
	r := newRig(t)
	sum := "Once"
	r.seed("once", caldav.Edit{Summary: &sum})
	tk, _ := r.cached("once")

	var cmd tea.Cmd
	r.m, cmd = r.m.startToggle(tk)
	if cmd == nil {
		t.Fatal("first write should start")
	}
	gen := r.srv.Gen(calPath + "once.ics")
	cur, _ := r.cached("once")
	var cmd2 tea.Cmd
	r.m, cmd2 = r.m.startToggle(cur)
	if cmd2 != nil || r.m.err == nil || !strings.Contains(r.m.err.Error(), "still saving") {
		t.Errorf("second write should be refused: cmd=%v err=%v", cmd2 != nil, r.m.err)
	}
	r.finish(cmd)
	if r.srv.Gen(calPath+"once.ics") != gen+1 {
		t.Errorf("server saw %d writes, want exactly one", r.srv.Gen(calPath+"once.ics")-gen)
	}
	// once confirmed, the task is writable again
	cur, _ = r.cached("once")
	r.m, cmd2 = r.m.startToggle(cur)
	if cmd2 == nil {
		t.Error("task should be writable again after the first write finished")
	}
}
