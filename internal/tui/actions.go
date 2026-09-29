package tui

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/chernesk/caldav-tui/internal/caldav"
	"github.com/chernesk/caldav-tui/internal/store"
)

// opMsg is the result of a write. On success the cache has already been
// updated; the model just reloads and moves the cursor.
type opMsg struct {
	task     *caldav.Task // written task (nil after a delete)
	status   string
	err      error
	conflict bool // server copy changed: re-sync
}

func newUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func fail(err error) opMsg {
	return opMsg{err: err, conflict: errors.Is(err, caldav.ErrConflict)}
}

// etagOf returns the cached ETag, or asks the server when the cache has none,
// so a write is always conditional on a known version.
func etagOf(ctx context.Context, cl *caldav.Client, t caldav.Task) (string, error) {
	if t.ETag != "" {
		return t.ETag, nil
	}
	e, err := cl.ETag(ctx, t.Href)
	if err != nil {
		return "", err
	}
	if e == "" {
		return "", errors.New("server returned no ETag; sync and try again")
	}
	return e, nil
}

// put writes raw over an existing task and refreshes the cache from what we wrote.
func put(ctx context.Context, cl *caldav.Client, st *store.Store, t caldav.Task, raw string) (caldav.Task, error) {
	etag, err := etagOf(ctx, cl, t)
	if err != nil {
		return t, err
	}
	newTag, err := cl.Put(ctx, t.Href, raw, etag)
	if err != nil {
		return t, err
	}
	return cache(st, t.Href, newTag, t.Calendar, raw)
}

func cache(st *store.Store, href, etag, calendar, raw string) (caldav.Task, error) {
	nt, err := caldav.TaskFromRaw(href, etag, calendar, raw)
	if err != nil {
		return nt, err
	}
	return nt, st.UpsertTask(nt)
}

func run(f func(ctx context.Context) opMsg) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return f(ctx)
	}
}

// toggleDone completes a task (advancing it if recurring) or reopens a completed one.
func (m Model) toggleDone(t caldav.Task) tea.Cmd {
	cl, st := m.cl, m.st
	return run(func(ctx context.Context) opMsg {
		now := time.Now()
		var raw, status string
		var err error
		switch {
		case t.Done():
			raw, err = caldav.SetDone(t.Raw, false, now)
			status = "reopened"
		default:
			var res caldav.CompleteResult
			raw, res, err = caldav.Complete(t.Raw, now)
			status = "completed"
			if res == caldav.Advanced {
				status = "done — repeats"
			}
		}
		if err != nil {
			return fail(err)
		}
		nt, err := put(ctx, cl, st, t, raw)
		if err != nil {
			return fail(err)
		}
		if status == "done — repeats" && !nt.Due.IsZero() {
			status = "done — next due " + dueLabel(nt)
		}
		return opMsg{task: &nt, status: status}
	})
}

// save creates a task (f.task == nil) or applies the form's edits to one.
func (m Model) save(f *form, e caldav.Edit) tea.Cmd {
	cl, st := m.cl, m.st
	existing := f.task
	parent, calendar := f.parent, f.calendar
	return run(func(ctx context.Context) opMsg {
		now := time.Now()
		if existing != nil {
			raw, err := caldav.ApplyEdit(existing.Raw, e, now)
			if err != nil {
				return fail(err)
			}
			nt, err := put(ctx, cl, st, *existing, raw)
			if err != nil {
				return fail(err)
			}
			return opMsg{task: &nt, status: "saved"}
		}
		uid := newUID()
		raw, err := caldav.NewRaw(uid, parent, e, now)
		if err != nil {
			return fail(err)
		}
		href := strings.TrimRight(calendar, "/") + "/" + uid + ".ics"
		etag, err := cl.Put(ctx, href, raw, "")
		if err != nil {
			return fail(err)
		}
		nt, err := cache(st, href, etag, calendar, raw)
		if err != nil {
			return fail(err)
		}
		return opMsg{task: &nt, status: "added"}
	})
}

func (m Model) remove(t caldav.Task) tea.Cmd {
	cl, st := m.cl, m.st
	return run(func(ctx context.Context) opMsg {
		etag, err := etagOf(ctx, cl, t)
		if err != nil {
			return fail(err)
		}
		if err := cl.Delete(ctx, t.Href, etag); err != nil {
			return fail(err)
		}
		if err := st.DeleteTask(t.Href); err != nil {
			return fail(err)
		}
		return opMsg{status: "deleted “" + truncate(t.Summary, 30) + "”"}
	})
}
