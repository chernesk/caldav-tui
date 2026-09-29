// Package ops holds the write operations shared by the TUI and reminders. A write
// is two-phase so the UI can be optimistic: Prepare computes the change without
// touching anything, Apply puts it in the local cache immediately, Push sends it to
// the server (ETag-guarded), and Revert undoes the cache change if the push fails.
package ops

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/chernesk/caldav-tui/internal/caldav"
	"github.com/chernesk/caldav-tui/internal/store"
)

type Kind int

const (
	Update Kind = iota
	Create
	Delete
)

// Pending is one change that has been (or is about to be) applied locally and
// still needs to reach the server.
type Pending struct {
	Kind Kind
	Prev *caldav.Task // cached row before the change (nil for Create)
	Next *caldav.Task // cached row after the change (nil for Delete)
	Raw  string       // iCalendar body to PUT (unused for Delete)
}

func (p *Pending) Href() string {
	if p.Next != nil {
		return p.Next.Href
	}
	return p.Prev.Href
}

// Outcome describes what a toggle did.
type Outcome int

const (
	Completed Outcome = iota
	Advanced          // recurring: moved to the next occurrence
	Reopened
)

func next(t caldav.Task, raw string) (*caldav.Task, error) {
	nt, err := caldav.TaskFromRaw(t.Href, t.ETag, t.Calendar, raw)
	if err != nil {
		return nil, err
	}
	return &nt, nil
}

// PrepareToggle completes a task (advancing it if recurring) or reopens a completed one.
func PrepareToggle(t caldav.Task, now time.Time) (*Pending, Outcome, error) {
	var raw string
	var out Outcome
	var err error
	if t.Done() {
		raw, err = caldav.SetDone(t.Raw, false, now)
		out = Reopened
	} else {
		var res caldav.CompleteResult
		raw, res, err = caldav.Complete(t.Raw, now)
		out = Completed
		if res == caldav.Advanced {
			out = Advanced
		}
	}
	if err != nil {
		return nil, out, err
	}
	n, err := next(t, raw)
	if err != nil {
		return nil, out, err
	}
	return &Pending{Kind: Update, Prev: &t, Next: n, Raw: raw}, out, nil
}

// PrepareEdit applies field edits to an existing task.
func PrepareEdit(t caldav.Task, e caldav.Edit, now time.Time) (*Pending, error) {
	raw, err := caldav.ApplyEdit(t.Raw, e, now)
	if err != nil {
		return nil, err
	}
	n, err := next(t, raw)
	if err != nil {
		return nil, err
	}
	return &Pending{Kind: Update, Prev: &t, Next: n, Raw: raw}, nil
}

// PrepareCreate builds a new task in a calendar. parent is a parent task UID or "".
func PrepareCreate(calendar, uid, parent string, e caldav.Edit, now time.Time) (*Pending, error) {
	raw, err := caldav.NewRaw(uid, parent, e, now)
	if err != nil {
		return nil, err
	}
	href := strings.TrimRight(calendar, "/") + "/" + uid + ".ics"
	nt, err := caldav.TaskFromRaw(href, "", calendar, raw)
	if err != nil {
		return nil, err
	}
	return &Pending{Kind: Create, Next: &nt, Raw: raw}, nil
}

func PrepareDelete(t caldav.Task) *Pending { return &Pending{Kind: Delete, Prev: &t} }

// Apply makes the change in the local cache.
func Apply(st *store.Store, p *Pending) error {
	if p.Kind == Delete {
		return st.DeleteTask(p.Prev.Href)
	}
	return st.UpsertTask(*p.Next)
}

// Revert undoes Apply, restoring the cache to what the server last told us.
func Revert(st *store.Store, p *Pending) error {
	switch p.Kind {
	case Create:
		return st.DeleteTask(p.Next.Href)
	default:
		return st.UpsertTask(*p.Prev)
	}
}

// ETagOf returns the cached ETag, or asks the server when the cache has none,
// so a write is always conditional on a known version.
func ETagOf(ctx context.Context, cl *caldav.Client, t caldav.Task) (string, error) {
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

// Push sends a pending change to the server. On success the cache row is updated
// with the server's new ETag. Errors wrap caldav.ErrConflict when the server copy
// changed underneath us.
func Push(ctx context.Context, cl *caldav.Client, st *store.Store, p *Pending) error {
	switch p.Kind {
	case Create:
		tag, err := cl.Put(ctx, p.Next.Href, p.Raw, "")
		if err != nil {
			return err
		}
		return setETag(st, p, tag)
	case Update:
		etag, err := ETagOf(ctx, cl, *p.Prev)
		if err != nil {
			return err
		}
		tag, err := cl.Put(ctx, p.Next.Href, p.Raw, etag)
		if err != nil {
			return err
		}
		return setETag(st, p, tag)
	default:
		etag, err := ETagOf(ctx, cl, *p.Prev)
		if err != nil {
			return err
		}
		return cl.Delete(ctx, p.Prev.Href, etag)
	}
}

func setETag(st *store.Store, p *Pending, tag string) error {
	nt := *p.Next
	nt.ETag = tag
	return st.UpsertTask(nt)
}
