package tui

import (
	"context"
	"time"

	"github.com/chernesk/caldav-tui/internal/caldav"
	"github.com/chernesk/caldav-tui/internal/store"
)

// Sync pulls every task calendar from the server into the cache.
func Sync(ctx context.Context, cl *caldav.Client, st *store.Store) error {
	cals, err := cl.Calendars(ctx)
	if err != nil {
		return err
	}
	for _, cal := range cals {
		tasks, err := cl.Tasks(ctx, cal)
		if err != nil {
			return err
		}
		if err := st.ReplaceCalendar(cal, tasks); err != nil {
			return err
		}
	}
	if err := st.PruneCalendars(cals); err != nil {
		return err
	}
	return st.SetMeta("last_sync", time.Now().Format(time.RFC3339))
}
