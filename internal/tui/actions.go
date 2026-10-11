package tui

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/chernesk/caldav-tui/internal/caldav"
	"github.com/chernesk/caldav-tui/internal/ops"
)

// Writes are optimistic: the change lands in the local cache and the list
// immediately, then is pushed to the server in the background. If the push fails
// the cache is rolled back and the error is shown.

// opMsg is the result of pushing one change to the server.
type opMsg struct {
	p        *ops.Pending
	err      error
	conflict bool // server copy changed underneath us: roll back and re-sync
}

func newUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// start applies a change locally right now and returns the command that pushes it.
func (m Model) start(p *ops.Pending, status string) (Model, tea.Cmd) {
	if p.Kind != ops.Create && m.pending[p.Href()] {
		m.err = errors.New("still saving that task; try again in a moment")
		return m, nil
	}
	if err := ops.Apply(m.st, p); err != nil {
		m.err = err
		return m, nil
	}
	m.pending[p.Href()] = true
	m.busy++
	m.err = nil
	m.flash = status
	if p.Next != nil && notStarted(*p.Next) && !m.showFuture {
		m.flash += " (hidden until start date; s shows)"
	}
	m.reload()
	if p.Next != nil {
		m.selectUID(p.Next.UID)
	}
	m.scroll()

	cl, st := m.cl, m.st
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := ops.Push(ctx, cl, st, p)
		return opMsg{p: p, err: err, conflict: errors.Is(err, caldav.ErrConflict)}
	}
}

func (m Model) startToggle(t caldav.Task) (Model, tea.Cmd) {
	p, out, err := ops.PrepareToggle(t, time.Now())
	if err != nil {
		m.err = err
		return m, nil
	}
	status := "completed"
	switch {
	case out == ops.Reopened:
		status = "reopened"
	case out == ops.Advanced && !p.Next.Due.IsZero():
		status = "done — next due " + dueLabel(*p.Next)
	case out == ops.Advanced:
		status = "done — repeats"
	}
	return m.start(p, status)
}

// startSave creates a task (f.task == nil) or applies the form's edits to one.
func (m Model) startSave(f *form, e caldav.Edit) (Model, tea.Cmd) {
	now := time.Now()
	if f.task == nil {
		if e.Priority == nil && m.defaultPrio != 0 {
			d := m.defaultPrio // a blank priority field means "use the default"; "none" is explicit
			e.Priority = &d
		}
		p, err := ops.PrepareCreate(f.calendar, newUID(), f.parent, e, now)
		if err != nil {
			m.err = err
			return m, nil
		}
		return m.start(p, "added")
	}
	// build on the freshest cached copy: a sync may have landed while the form was open
	base := *f.task
	if fresh, ok := m.st.Task(base.Href); ok {
		base = fresh
	}
	p, err := ops.PrepareEdit(base, e, now)
	if err != nil {
		m.err = err
		return m, nil
	}
	return m.start(p, "saved")
}

func (m Model) startDelete(t caldav.Task) (Model, tea.Cmd) {
	return m.start(ops.PrepareDelete(t), "deleted “"+truncate(t.Summary, 30)+"”")
}

// opDone handles the server's answer. Success needs nothing more (the cache
// already holds the change); failure rolls the cache back.
func (m Model) opDone(msg opMsg) (tea.Model, tea.Cmd) {
	m.busy = max(0, m.busy-1)
	delete(m.pending, msg.p.Href())
	if msg.err == nil {
		m.reload()
		m.scroll()
		return m, nil
	}
	_ = ops.Revert(m.st, msg.p)
	m.flash = ""
	m.reload()
	m.scroll()
	if msg.conflict {
		m.err = errors.New("changed on the server since last sync; change undone, refreshing — try again")
		if !m.syncing {
			m.syncing = true
			return m, m.sync()
		}
		return m, nil
	}
	m.err = fmt.Errorf("not saved (change undone): %w", msg.err)
	return m, nil
}
