// Package store is the local SQLite cache the UI reads from.
package store

import (
	"database/sql"
	"time"

	"github.com/chernesk/caldav-tui/internal/caldav"
	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS calendars (
	path TEXT PRIMARY KEY,
	name TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS tasks (
	href       TEXT PRIMARY KEY,
	calendar   TEXT NOT NULL,
	etag       TEXT NOT NULL,
	uid        TEXT NOT NULL,
	summary    TEXT NOT NULL,
	notes      TEXT NOT NULL,
	status     TEXT NOT NULL,
	priority   INTEGER NOT NULL,
	due        INTEGER,          -- unix seconds
	due_all_day INTEGER NOT NULL,
	start      INTEGER,
	completed  INTEGER,
	rrule      TEXT NOT NULL,
	parent     TEXT NOT NULL,
	raw        TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS tasks_calendar ON tasks(calendar);
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
`

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func unix(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Unix()
}

func fromUnix(n sql.NullInt64) time.Time {
	if !n.Valid {
		return time.Time{}
	}
	return time.Unix(n.Int64, 0)
}

// ReplaceCalendar makes the cache for one calendar match the server's task set.
func (s *Store) ReplaceCalendar(cal caldav.Calendar, tasks []caldav.Task) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO calendars(path,name) VALUES(?,?) ON CONFLICT(path) DO UPDATE SET name=excluded.name`, cal.Path, cal.Name); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM tasks WHERE calendar=?`, cal.Path); err != nil {
		return err
	}
	for _, t := range tasks {
		if _, err := tx.Exec(`INSERT INTO tasks
			(href,calendar,etag,uid,summary,notes,status,priority,due,due_all_day,start,completed,rrule,parent,raw)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(href) DO UPDATE SET calendar=excluded.calendar, etag=excluded.etag, uid=excluded.uid,
			summary=excluded.summary, notes=excluded.notes, status=excluded.status, priority=excluded.priority,
			due=excluded.due, due_all_day=excluded.due_all_day, start=excluded.start, completed=excluded.completed,
			rrule=excluded.rrule, parent=excluded.parent, raw=excluded.raw`,
			t.Href, cal.Path, t.ETag, t.UID, t.Summary, t.Notes, t.Status, t.Priority,
			unix(t.Due), b2i(t.DueAllDay), unix(t.Start), unix(t.Completed), t.RRule, t.Parent, t.Raw); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PruneCalendars drops calendars (and their tasks) no longer on the server.
func (s *Store) PruneCalendars(keep []caldav.Calendar) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	have := map[string]bool{}
	for _, c := range keep {
		have[c.Path] = true
	}
	rows, err := tx.Query(`SELECT path FROM calendars`)
	if err != nil {
		return err
	}
	var drop []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return err
		}
		if !have[p] {
			drop = append(drop, p)
		}
	}
	rows.Close()
	for _, p := range drop {
		if _, err := tx.Exec(`DELETE FROM tasks WHERE calendar=?`, p); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM calendars WHERE path=?`, p); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Calendars() ([]caldav.Calendar, error) {
	rows, err := s.db.Query(`SELECT path,name FROM calendars ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []caldav.Calendar
	for rows.Next() {
		var c caldav.Calendar
		if err := rows.Scan(&c.Path, &c.Name); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Tasks returns cached tasks; calendar "" means all.
func (s *Store) Tasks(calendar string) ([]caldav.Task, error) {
	q := `SELECT href,calendar,etag,uid,summary,notes,status,priority,due,due_all_day,start,completed,rrule,parent,raw FROM tasks`
	var args []any
	if calendar != "" {
		q += ` WHERE calendar=?`
		args = append(args, calendar)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []caldav.Task
	for rows.Next() {
		var t caldav.Task
		var due, start, comp sql.NullInt64
		var allDay int
		if err := rows.Scan(&t.Href, &t.Calendar, &t.ETag, &t.UID, &t.Summary, &t.Notes, &t.Status, &t.Priority,
			&due, &allDay, &start, &comp, &t.RRule, &t.Parent, &t.Raw); err != nil {
			return nil, err
		}
		t.Due, t.Start, t.Completed = fromUnix(due), fromUnix(start), fromUnix(comp)
		t.DueAllDay = allDay != 0
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) SetMeta(k, v string) error {
	_, err := s.db.Exec(`INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v)
	return err
}

func (s *Store) Meta(k string) string {
	var v string
	_ = s.db.QueryRow(`SELECT value FROM meta WHERE key=?`, k).Scan(&v)
	return v
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
