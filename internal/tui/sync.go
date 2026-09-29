package tui

import (
	"context"

	"github.com/chernesk/caldav-tui/internal/store"
	"github.com/chernesk/caldav-tui/internal/syncer"
)

// Sync brings the cache up to date from the server.
func Sync(ctx context.Context, src syncer.Source, st *store.Store) (*syncer.Result, error) {
	return syncer.Sync(ctx, src, st)
}
