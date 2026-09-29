// Package syncer brings the local cache in line with the server, downloading as
// little as possible: sync-collection deltas (RFC 6578) when the server has them,
// ETag diffing when it doesn't.
package syncer

import (
	"context"
	"errors"
	"time"

	"github.com/chernesk/caldav-tui/internal/caldav"
	"github.com/chernesk/caldav-tui/internal/store"
)

// Source is the server side of a sync (implemented by *caldav.Client).
type Source interface {
	Calendars(ctx context.Context) ([]caldav.Calendar, error)
	SyncCollection(ctx context.Context, calPath, token string) (*caldav.Delta, error)
	ListETags(ctx context.Context, calPath string) ([]caldav.Change, error)
	FetchTasks(ctx context.Context, cal caldav.Calendar, hrefs []string) ([]caldav.Task, error)
}

// Mode says how a calendar was synced.
type Mode string

const (
	ModeDelta Mode = "delta" // sync-collection with a stored token
	ModeFull  Mode = "full"  // sync-collection from scratch (first run, or token expired)
	ModeETag  Mode = "etag"  // server lacks sync-collection: diffed ETags
	ModeNoop  Mode = "no-op" // nothing changed
)

// Result reports what a sync did, per calendar.
type Result struct {
	Calendars []CalResult
}

type CalResult struct {
	Name    string
	Mode    Mode
	Fetched int // task bodies downloaded
	Deleted int
	Total   int // tasks on the server (delta: unknown, -1)
}

// Sync syncs every task calendar and prunes calendars that disappeared.
func Sync(ctx context.Context, src Source, st *store.Store) (*Result, error) {
	cals, err := src.Calendars(ctx)
	if err != nil {
		return nil, err
	}
	res := &Result{}
	for _, cal := range cals {
		r, err := SyncCalendar(ctx, src, st, cal)
		if err != nil {
			return res, err
		}
		res.Calendars = append(res.Calendars, r)
	}
	if err := st.PruneCalendars(cals); err != nil {
		return res, err
	}
	return res, st.SetMeta("last_sync", time.Now().Format(time.RFC3339))
}

// SyncCalendar syncs one calendar.
func SyncCalendar(ctx context.Context, src Source, st *store.Store, cal caldav.Calendar) (CalResult, error) {
	out := CalResult{Name: cal.Name, Total: -1}
	cached, err := st.ETags(cal.Path)
	if err != nil {
		return out, err
	}

	var changed []caldav.Change
	var deleted []string
	newToken := ""
	full := true // true when `changed` is the server's complete list

	if token := st.SyncToken(cal.Path); token != "" {
		d, err := src.SyncCollection(ctx, cal.Path, token)
		switch {
		case err == nil:
			changed, deleted, newToken, full = d.Changed, d.Deleted, d.Token, false
			out.Mode = ModeDelta
		case errors.Is(err, caldav.ErrSyncTokenInvalid), errors.Is(err, caldav.ErrSyncUnsupported):
			// fall through to a full resync below
		default:
			return out, err
		}
	}

	if full {
		d, err := src.SyncCollection(ctx, cal.Path, "")
		switch {
		case err == nil:
			changed, newToken = d.Changed, d.Token
			out.Mode = ModeFull
		case errors.Is(err, caldav.ErrSyncUnsupported):
			changed, err = src.ListETags(ctx, cal.Path)
			if err != nil {
				return out, err
			}
			newToken = ""
			out.Mode = ModeETag
		default:
			return out, err
		}
		// anything cached that the server no longer lists was deleted
		live := make(map[string]bool, len(changed))
		for _, c := range changed {
			live[c.Href] = true
		}
		for h := range cached {
			if !live[h] {
				deleted = append(deleted, h)
			}
		}
		out.Total = len(changed)
	}

	// only download what actually differs from the cache
	var need []string
	for _, c := range changed {
		if c.ETag == "" || cached[c.Href] != c.ETag {
			need = append(need, c.Href)
		}
	}
	var tasks []caldav.Task
	if len(need) > 0 {
		if tasks, err = src.FetchTasks(ctx, cal, need); err != nil {
			return out, err
		}
	}
	// deletes only matter for things we have
	var realDeleted []string
	for _, h := range deleted {
		if _, ok := cached[h]; ok {
			realDeleted = append(realDeleted, h)
		}
	}

	if err := st.ApplyDelta(cal, tasks, realDeleted, newToken); err != nil {
		return out, err
	}
	out.Fetched, out.Deleted = len(tasks), len(realDeleted)
	if out.Mode == ModeDelta && out.Fetched == 0 && out.Deleted == 0 {
		out.Mode = ModeNoop
	}
	return out, nil
}
