package tui

import (
	"testing"
	"time"

	"github.com/chernesk/caldav-tui/internal/caldav"
)

func TestBuildTree(t *testing.T) {
	d := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	tasks := []caldav.Task{
		{UID: "kid", Summary: "kid", Parent: "p", Status: "NEEDS-ACTION"},
		{UID: "later", Summary: "later", Due: d.AddDate(0, 0, 5), Status: "NEEDS-ACTION"},
		{UID: "p", Summary: "parent", Due: d, Status: "NEEDS-ACTION"},
		{UID: "orphan", Summary: "orphan", Parent: "missing", Status: "NEEDS-ACTION"},
		{UID: "loopA", Parent: "loopB", Status: "NEEDS-ACTION"},
		{UID: "loopB", Parent: "loopA", Status: "NEEDS-ACTION"},
	}
	var got []string
	for _, r := range buildTree(tasks, 0) {
		got = append(got, r.task.UID)
	}
	want := []string{"p", "kid", "later", "orphan"}
	for i, w := range want {
		if i >= len(got) || got[i] != w {
			t.Fatalf("order = %v, want prefix %v", got, want)
		}
	}
}

func TestNotStarted(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name  string
		start time.Time
		want  bool
	}{
		{"no start", time.Time{}, false},
		{"yesterday", now.AddDate(0, 0, -1), false},
		{"later today", startOfDay(now).Add(23 * time.Hour), false},
		{"tomorrow", now.AddDate(0, 0, 1), true},
	}
	for _, c := range cases {
		if got := notStarted(caldav.Task{Start: c.start}); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
