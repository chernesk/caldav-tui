package tui

import (
	"strings"
	"testing"

	"github.com/chernesk/caldav-tui/internal/caldav"
)

func nt(uid, parent string, done bool) caldav.Task {
	t := caldav.Task{UID: uid, Summary: uid, Parent: parent, Status: "NEEDS-ACTION"}
	if done {
		t.Status = "COMPLETED"
	}
	return t
}

func TestTreeGuidesAndCollapse(t *testing.T) {
	tasks := []caldav.Task{nt("p", "", false), nt("a", "p", false), nt("b", "p", false), nt("a1", "a", false), nt("z", "", false)}
	rows := buildTree(tasks, nil)
	var got []string
	for _, r := range rows {
		got = append(got, r.prefix+r.task.UID)
	}
	want := []string{"p", "├ a", "│ └ a1", "└ b", "z"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("guides:\n got %q\nwant %q", got, want)
	}

	rows = buildTree(tasks, map[string]bool{"p": true})
	if len(rows) != 2 || rows[0].task.UID != "p" || !rows[0].collapsed || rows[1].task.UID != "z" {
		t.Errorf("collapsed parent should hide all descendants: %+v", rows)
	}
	// collapsing a leaf (or unknown uid) changes nothing
	if rows = buildTree(tasks, map[string]bool{"z": true, "nope": true}); len(rows) != 5 {
		t.Errorf("rows = %d", len(rows))
	}
}

// seed a parent with two children, one of them done
func seedFamily(r *rig) {
	p, a, b := "Walmart", "Blanket", "Food"
	r.seed("par", caldav.Edit{Summary: &p})
	r.cursorOn("par")
	r.key("A")
	r.key(a)
	r.key("enter")
	r.cursorOn("par")
	r.key("A")
	r.key(b)
	r.key("enter")
}

func TestProgressCountsIncludeHiddenCompleted(t *testing.T) {
	r := newRig(t)
	seedFamily(r)
	if len(r.m.rows) != 3 {
		t.Fatalf("rows = %d", len(r.m.rows))
	}
	// complete Blanket: it disappears (completed tasks are hidden) but still counts
	for i, row := range r.m.rows {
		if row.task.Summary == "Blanket" {
			r.m.cursor = i
		}
	}
	r.key("x")
	if len(r.m.rows) != 2 {
		t.Fatalf("completed child should be hidden, rows = %d", len(r.m.rows))
	}
	line := r.m.renderRow(r.m.rows[0])
	if !strings.Contains(line, "Walmart") || !strings.Contains(line, "(1/2)") {
		t.Errorf("parent row should show progress 1/2: %q", line)
	}
	r.cursorOn("par")
	if d := r.m.renderDetail(); !strings.Contains(d, "1 of 2 subtasks done") {
		t.Errorf("detail: %q", d)
	}
}

func TestFoldKeysAndPersistence(t *testing.T) {
	r := newRig(t)
	seedFamily(r)
	r.cursorOn("par")

	r.key("h") // collapse
	if len(r.m.rows) != 1 || !r.m.rows[0].collapsed {
		t.Fatalf("h should collapse the parent: %d rows", len(r.m.rows))
	}
	if !strings.Contains(r.m.renderRow(r.m.rows[0]), "▸") {
		t.Error("collapsed parent should carry a ▸")
	}
	if !r.st.Collapsed()["par"] {
		t.Error("fold state not persisted")
	}
	// a fresh model on the same cache comes up folded
	if m2 := New(r.m.cl, r.st); len(m2.rows) != 1 {
		t.Errorf("fold state lost across restarts: %d rows", len(m2.rows))
	}

	r.key("l") // expand
	if len(r.m.rows) != 3 || r.st.Collapsed()["par"] {
		t.Errorf("l should expand: %d rows", len(r.m.rows))
	}
	r.key("z") // toggle
	if len(r.m.rows) != 1 {
		t.Error("z should collapse")
	}
	r.key("z")
	if len(r.m.rows) != 3 {
		t.Error("z should expand")
	}

	// h on a child jumps to its parent, like a file tree
	r.m.cursor = 2
	r.key("h")
	if r.m.rows[r.m.cursor].task.UID != "par" {
		t.Errorf("h on a child should select the parent, got %q", r.m.rows[r.m.cursor].task.Summary)
	}
	// h/l/z on a task with no subtasks do nothing harmful
	r.m.cursor = 1
	r.key("l")
	r.key("z")
	if len(r.m.rows) != 3 {
		t.Error("l/z on a leaf must not change the list")
	}
}

func TestDeletePromptWarnsAboutSubtasks(t *testing.T) {
	r := newRig(t)
	seedFamily(r)
	r.cursorOn("par")
	r.key("d")
	v := r.m.View()
	if !strings.Contains(v, "2 subtask") || !strings.Contains(v, "NOT deleted") {
		t.Errorf("prompt should warn that subtasks stay:\n%s", v)
	}
	r.key("n") // cancel: nothing may be deleted
	if len(r.m.rows) != 3 {
		t.Error("cancelling must leave everything in place")
	}
}

func TestSubtaskWrittenLikeTasksOrg(t *testing.T) {
	r := newRig(t)
	seedFamily(r)
	var child caldav.Task
	for _, row := range r.m.rows {
		if row.depth == 1 {
			child = row.task
			break
		}
	}
	if !strings.Contains(r.server(child.UID), "RELATED-TO;RELTYPE=PARENT:par") {
		t.Errorf("child link not in Tasks.org's form:\n%s", r.server(child.UID))
	}
}
