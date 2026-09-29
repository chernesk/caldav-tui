package syncer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chernesk/caldav-tui/internal/caldav"
	"github.com/chernesk/caldav-tui/internal/store"
)

const calPath = "/dav/cal/"

var cal = caldav.Calendar{Path: calPath, Name: "Tasks"}

type event struct {
	ver     int
	href    string
	deleted bool
}

// fakeSrc is an in-memory server: a set of tasks plus a change journal for deltas.
type fakeSrc struct {
	ver         int
	objs        map[string]caldav.Task
	journal     []event
	invalidate  bool // reject tokens as expired
	noSyncColl  bool // server lacks sync-collection
	failFetch   bool
	fetchedHref []string
	syncCalls   int
}

func newSrc() *fakeSrc { return &fakeSrc{objs: map[string]caldav.Task{}} }

func (f *fakeSrc) put(uid, summary string) string {
	f.ver++
	href := calPath + uid + ".ics"
	f.objs[href] = caldav.Task{Href: href, ETag: fmt.Sprintf(`"e%d"`, f.ver), Calendar: calPath, UID: uid, Summary: summary, Status: "NEEDS-ACTION"}
	f.journal = append(f.journal, event{f.ver, href, false})
	return href
}

func (f *fakeSrc) del(uid string) {
	f.ver++
	href := calPath + uid + ".ics"
	delete(f.objs, href)
	f.journal = append(f.journal, event{f.ver, href, true})
}

func (f *fakeSrc) Calendars(context.Context) ([]caldav.Calendar, error) {
	return []caldav.Calendar{cal}, nil
}

func (f *fakeSrc) changes(all bool, since int) *caldav.Delta {
	d := &caldav.Delta{Token: "v" + strconv.Itoa(f.ver)}
	if all {
		for _, t := range f.objs {
			d.Changed = append(d.Changed, caldav.Change{Href: t.Href, ETag: t.ETag})
		}
		return d
	}
	last := map[string]event{}
	for _, e := range f.journal {
		if e.ver > since {
			last[e.href] = e
		}
	}
	for h, e := range last {
		if e.deleted {
			d.Deleted = append(d.Deleted, h)
		} else {
			d.Changed = append(d.Changed, caldav.Change{Href: h, ETag: f.objs[h].ETag})
		}
	}
	return d
}

func (f *fakeSrc) SyncCollection(_ context.Context, _, token string) (*caldav.Delta, error) {
	f.syncCalls++
	if f.noSyncColl {
		return nil, caldav.ErrSyncUnsupported
	}
	if token == "" {
		return f.changes(true, 0), nil
	}
	if f.invalidate {
		return nil, caldav.ErrSyncTokenInvalid
	}
	n, _ := strconv.Atoi(strings.TrimPrefix(token, "v"))
	return f.changes(false, n), nil
}

func (f *fakeSrc) ListETags(context.Context, string) ([]caldav.Change, error) {
	return f.changes(true, 0).Changed, nil
}

func (f *fakeSrc) FetchTasks(_ context.Context, _ caldav.Calendar, hrefs []string) ([]caldav.Task, error) {
	if f.failFetch {
		return nil, errors.New("network down")
	}
	var out []caldav.Task
	for _, h := range hrefs {
		f.fetchedHref = append(f.fetchedHref, h)
		if t, ok := f.objs[h]; ok {
			t.Raw = "BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n"
			out = append(out, t)
		}
	}
	return out, nil
}

func newStore(t *testing.T) *store.Store {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func run(t *testing.T, src *fakeSrc, st *store.Store) CalResult {
	t.Helper()
	src.fetchedHref = nil
	res, err := Sync(context.Background(), src, st)
	if err != nil {
		t.Fatal(err)
	}
	return res.Calendars[0]
}

func summaries(t *testing.T, st *store.Store) string {
	ts, _ := st.Tasks("")
	var s []string
	for _, x := range ts {
		s = append(s, x.Summary)
	}
	// stable order
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
	return strings.Join(s, ",")
}

func TestFirstSyncIsFullThenDelta(t *testing.T) {
	src, st := newSrc(), newStore(t)
	src.put("a", "A")
	src.put("b", "B")
	src.put("c", "C")

	r := run(t, src, st)
	if r.Mode != ModeFull || r.Fetched != 3 || r.Total != 3 {
		t.Errorf("first sync: %+v", r)
	}
	if st.SyncToken(calPath) != "v3" {
		t.Errorf("token = %q", st.SyncToken(calPath))
	}

	// nothing changed
	r = run(t, src, st)
	if r.Mode != ModeNoop || r.Fetched != 0 || len(src.fetchedHref) != 0 {
		t.Errorf("no-op sync: %+v fetched=%v", r, src.fetchedHref)
	}

	// one edit, one delete, one add: fetch only the two live ones
	src.put("b", "B2")
	src.del("c")
	src.put("d", "D")
	r = run(t, src, st)
	if r.Mode != ModeDelta || r.Fetched != 2 || r.Deleted != 1 {
		t.Errorf("delta: %+v", r)
	}
	if got := summaries(t, st); got != "A,B2,D" {
		t.Errorf("cache = %s", got)
	}
	if len(src.fetchedHref) != 2 {
		t.Errorf("downloaded %v; should be just b and d", src.fetchedHref)
	}
}

func TestOwnWriteIsNotRedownloaded(t *testing.T) {
	src, st := newSrc(), newStore(t)
	href := src.put("a", "A")
	run(t, src, st)

	// we edit through the app: the cache gets the new ETag, the server bumps its token
	src.put("a", "A edited")
	mine := src.objs[href]
	mine.Raw = "BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n"
	if err := st.UpsertTask(mine); err != nil {
		t.Fatal(err)
	}
	r := run(t, src, st)
	if r.Fetched != 0 {
		t.Errorf("re-downloaded our own write: %+v %v", r, src.fetchedHref)
	}
	if r.Mode != ModeNoop {
		t.Errorf("mode = %s", r.Mode)
	}
}

func TestExpiredTokenFullResyncCatchesDeletes(t *testing.T) {
	src, st := newSrc(), newStore(t)
	src.put("a", "A")
	src.put("b", "B")
	run(t, src, st)

	src.del("b")
	src.put("c", "C")
	src.invalidate = true
	r := run(t, src, st)
	if r.Mode != ModeFull || r.Deleted != 1 {
		t.Errorf("resync: %+v", r)
	}
	if got := summaries(t, st); got != "A,C" {
		t.Errorf("cache = %s", got)
	}
	// only the new one is downloaded: a's ETag already matched
	if len(src.fetchedHref) != 1 {
		t.Errorf("downloaded %v", src.fetchedHref)
	}
	if st.SyncToken(calPath) == "" {
		t.Error("no fresh token after resync")
	}
}

func TestServerWithoutSyncCollectionUsesETagDiff(t *testing.T) {
	src, st := newSrc(), newStore(t)
	src.noSyncColl = true
	src.put("a", "A")
	src.put("b", "B")

	r := run(t, src, st)
	if r.Mode != ModeETag || r.Fetched != 2 {
		t.Errorf("first: %+v", r)
	}
	if st.SyncToken(calPath) != "" {
		t.Error("stored a token for a server that has none")
	}

	src.put("a", "A2")
	src.del("b")
	src.put("c", "C")
	r = run(t, src, st)
	if r.Mode != ModeETag || r.Fetched != 2 || r.Deleted != 1 {
		t.Errorf("second: %+v", r)
	}
	if got := summaries(t, st); got != "A2,C" {
		t.Errorf("cache = %s", got)
	}
	r = run(t, src, st)
	if r.Fetched != 0 || r.Deleted != 0 {
		t.Errorf("idle: %+v", r)
	}
}

func TestFailedFetchDoesNotAdvanceToken(t *testing.T) {
	src, st := newSrc(), newStore(t)
	src.put("a", "A")
	run(t, src, st)
	before := st.SyncToken(calPath)

	src.put("b", "B")
	src.failFetch = true
	if _, err := Sync(context.Background(), src, st); err == nil {
		t.Fatal("expected error")
	}
	if st.SyncToken(calPath) != before {
		t.Errorf("token advanced to %q despite failure; changes would be lost", st.SyncToken(calPath))
	}
	// recovers on the next attempt
	src.failFetch = false
	r := run(t, src, st)
	if r.Fetched != 1 || summaries(t, st) != "A,B" {
		t.Errorf("recovery: %+v cache=%s", r, summaries(t, st))
	}
}

func TestDeleteOfUnknownObjectIsIgnored(t *testing.T) {
	src, st := newSrc(), newStore(t)
	src.put("a", "A")
	run(t, src, st)
	src.put("x", "X")
	src.del("x") // created and deleted between syncs: we never had it
	r := run(t, src, st)
	if r.Deleted != 0 || summaries(t, st) != "A" {
		t.Errorf("%+v cache=%s", r, summaries(t, st))
	}
	_ = time.Now
}
