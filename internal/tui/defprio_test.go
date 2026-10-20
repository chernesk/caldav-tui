package tui

import (
	"strings"
	"testing"

	"github.com/chernesk/caldav-tui/internal/caldav"
)

func TestQuickDefaultPriority(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"buy milk", 9},       // nothing said: the default
		{"buy milk !high", 1}, // explicit wins
		{"buy milk !low", 9},
		{"buy milk !none", 0}, // explicit "no priority" beats the default
		{"buy milk !!", 1},
	}
	for _, c := range cases {
		q, err := QuickParse(c.in, mon)
		if err != nil {
			t.Fatal(err)
		}
		q = q.WithDefault(9)
		if q.Priority != c.want {
			t.Errorf("%q: priority %d, want %d", c.in, q.Priority, c.want)
		}
		if e := q.Edit(); c.want != 0 && (e.Priority == nil || *e.Priority != c.want) {
			t.Errorf("%q: edit priority %v", c.in, e.Priority)
		}
	}
	// no default configured: behaviour unchanged
	q, _ := QuickParse("buy milk", mon)
	if q = q.WithDefault(0); q.Priority != 0 || q.Edit().Priority != nil {
		t.Errorf("unexpected priority without a default: %+v", q)
	}
}

func priorityLine(raw string) string {
	for _, l := range strings.Split(raw, "\r\n") {
		if strings.HasPrefix(l, "PRIORITY:") {
			return l
		}
	}
	return ""
}

func TestFlowDefaultPriorityOnNewTasks(t *testing.T) {
	r := newRig(t)
	r.m = r.m.WithDefaultPriority(9)

	add := func(typed string) caldav.Task {
		r.t.Helper()
		before := len(r.m.rows)
		r.key("a")
		r.key(typed)
		r.key("enter")
		if len(r.m.rows) != before+1 {
			r.t.Fatalf("%q: task not added (rows %d -> %d, err %v)", typed, before, len(r.m.rows), r.m.err)
		}
		for _, row := range r.m.rows {
			if row.task.Summary == strings.Fields(typed)[0]+" "+strings.Fields(typed)[1] || strings.HasPrefix(typed, row.task.Summary) {
				return row.task
			}
		}
		r.t.Fatalf("%q not found", typed)
		return caldav.Task{}
	}

	if got := priorityLine(r.server(add("Buy milk").UID)); got != "PRIORITY:9" {
		t.Errorf("default: %q", got)
	}
	if got := priorityLine(r.server(add("Buy eggs !none").UID)); got != "" {
		t.Errorf("!none should leave no priority, got %q", got)
	}
	if got := priorityLine(r.server(add("Buy tea !high").UID)); got != "PRIORITY:1" {
		t.Errorf("explicit: %q", got)
	}
}

func TestFlowDefaultPriorityInFullForm(t *testing.T) {
	r := newRig(t)
	r.m = r.m.WithDefaultPriority(9)

	// quick prompt -> tab: the form opens with the default visible
	r.key("a")
	r.key("Form task")
	r.key("tab")
	if got := r.m.form.text(fPrio); got != "low" {
		t.Fatalf("form priority prefill = %q, want low", got)
	}
	r.key("enter")
	tk := r.m.rows[0].task
	if got := priorityLine(r.server(tk.UID)); got != "PRIORITY:9" {
		t.Errorf("saved default: %q", got)
	}

	// clearing the field means "use the default"; typing none means none
	for _, c := range []struct{ name, set, want string }{{"blank", "", "PRIORITY:9"}, {"none", "none", ""}} {
		r.key("a")
		r.key("Task " + c.name)
		r.key("tab")
		r.m.form.inputs[fPrio].SetValue(c.set)
		r.key("enter")
		var uid string
		for _, row := range r.m.rows {
			if row.task.Summary == "Task "+c.name {
				uid = row.task.UID
			}
		}
		if uid == "" {
			t.Fatalf("%s: not added", c.name)
		}
		if got := priorityLine(r.server(uid)); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestEditingDoesNotApplyDefault(t *testing.T) {
	r := newRig(t)
	sum := "Old task"
	r.seed("old", caldav.Edit{Summary: &sum}) // no priority
	r.m = r.m.WithDefaultPriority(9)
	r.cursorOn("old")
	r.key("e")
	r.key(" renamed")
	r.key("enter")
	raw := r.server("old")
	if !strings.Contains(raw, "SUMMARY:Old task renamed") {
		t.Fatalf("edit failed:\n%s", raw)
	}
	if got := priorityLine(raw); got != "" {
		t.Errorf("editing an existing task must not add a priority, got %q", got)
	}
}

func TestNoPriorityMarkerAndSort(t *testing.T) {
	mk := func(uid string, prio int) caldav.Task {
		return caldav.Task{UID: uid, Summary: uid, Priority: prio, Status: "NEEDS-ACTION"}
	}
	// no priority always sorts last, below low
	var got []string
	for _, r := range buildTree([]caldav.Task{mk("a-none", 0), mk("b-low", 9), mk("c-med", 5), mk("d-high", 1)}, nil) {
		got = append(got, r.task.UID)
	}
	if want := "d-high,c-med,b-low,a-none"; strings.Join(got, ",") != want {
		t.Errorf("order = %v, want %s", got, want)
	}

	render := func(def, prio int) string {
		m := Model{w: 100, defaultPrio: def}
		return m.renderRow(row{task: mk("x", prio)})
	}
	// default low: low is the baseline (blank), none stands out
	if strings.Contains(render(9, 9), "↓") || strings.Contains(render(9, 9), "–") {
		t.Error("default-level tasks should carry no marker")
	}
	if !strings.Contains(render(9, 0), "–") {
		t.Error("no-priority tasks should be marked when a default is set")
	}
	if !strings.Contains(render(9, 5), "!") || !strings.Contains(render(9, 1), "!!") {
		t.Error("medium/high markers lost")
	}
	// no default configured: no priority is normal, low keeps its arrow
	if strings.Contains(render(0, 0), "–") {
		t.Error("without a default, no-priority should not be marked")
	}
	if !strings.Contains(render(0, 9), "↓") {
		t.Error("low should show ↓ when low is not the default")
	}
}

func TestDetailShowsPriority(t *testing.T) {
	m := Model{w: 100, defaultPrio: 9}
	m.rows = []row{{task: caldav.Task{UID: "x", Summary: "x", Status: "NEEDS-ACTION"}}}
	if !strings.Contains(m.renderDetail(), "no priority") {
		t.Error("detail should say no priority")
	}
	m.rows[0].task.Priority = 1
	if !strings.Contains(m.renderDetail(), "high priority") {
		t.Error("detail should show the priority")
	}
	m.defaultPrio = 0
	m.rows[0].task.Priority = 0
	if strings.Contains(m.renderDetail(), "priority") {
		t.Error("without a default, don't mention a missing priority")
	}
}
