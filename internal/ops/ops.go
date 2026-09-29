// Package ops holds the write operations shared by the TUI and the notification
// daemon, so both apply the same ETag-guarded, patch-only edits.
package ops

import (
	"context"
	"errors"
	"time"

	"github.com/chernesk/caldav-tui/internal/caldav"
	"github.com/chernesk/caldav-tui/internal/store"
)

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

// Put writes raw over an existing task and refreshes the cache from what we wrote.
func Put(ctx context.Context, cl *caldav.Client, st *store.Store, t caldav.Task, raw string) (caldav.Task, error) {
	etag, err := ETagOf(ctx, cl, t)
	if err != nil {
		return t, err
	}
	newTag, err := cl.Put(ctx, t.Href, raw, etag)
	if err != nil {
		return t, err
	}
	return Cache(st, t.Href, newTag, t.Calendar, raw)
}

// Cache parses raw and stores it as the task at href.
func Cache(st *store.Store, href, etag, calendar, raw string) (caldav.Task, error) {
	nt, err := caldav.TaskFromRaw(href, etag, calendar, raw)
	if err != nil {
		return nt, err
	}
	return nt, st.UpsertTask(nt)
}

// Outcome describes what ToggleDone did.
type Outcome int

const (
	Completed Outcome = iota
	Advanced          // recurring: moved to the next occurrence
	Reopened
)

// ToggleDone completes a task (advancing it if recurring) or reopens a completed one.
func ToggleDone(ctx context.Context, cl *caldav.Client, st *store.Store, t caldav.Task, now time.Time) (caldav.Task, Outcome, error) {
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
		return t, out, err
	}
	nt, err := Put(ctx, cl, st, t, raw)
	return nt, out, err
}
