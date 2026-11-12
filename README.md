# caldav-tui

A terminal task manager that talks CalDAV (VTODO) directly — no vdirsyncer, no directory of `.ics` files.
Reads come from a local SQLite cache (`~/.cache/caldav-tui/tasks.db`); sync runs in the background.

## Setup

Create `~/.config/caldav-tui/config.json` (use a Fastmail app password with CalDAV access):

    {
      "server": "https://caldav.fastmail.com",
      "username": "you@fastmail.com",
      "password_cmd": "secret-tool lookup service caldav-tui"
    }

`password` works too, but `password_cmd` keeps the secret out of the file.

## Build & run

    go build -o caldav-tui . && ./caldav-tui
    ./caldav-tui sync      # one-shot sync, prints task counts per calendar
    ./caldav-tui version   # also shown at the end of the footer

The version comes from git: release with `git tag vX.Y.Z` and build. Builds with uncommitted changes or past
the last tag show a `+dirty` / pseudo-version suffix.

## Keys

List: h/l fold/unfold a parent (h on a subtask jumps to its parent) · z toggle fold · j/k move · ctrl-d/u half page · g/G top/bottom · tab/shift-tab calendar · c show completed ·
s show unstarted (start date in the future; hidden by default) · r refresh · q quit

Actions: x or space complete/reopen (recurring tasks advance to the next occurrence) · a quick-add ·
A quick-add a subtask under the selected task · e or enter edit · d delete (asks y/N)

Form: tab/shift-tab or up/down between fields · enter or ctrl-s save · esc cancel. Dates take
`today`, `tomorrow 2pm`, `fri`, `+3d`, `oct 5`, `2026-10-05 14:30`; repeat takes `daily`,
`weekly mon,wed`, `every 2 weeks`, `monthly`, `weekdays`, or a raw `FREQ=...` rule.

## Nested tasks

Subtasks are linked the way Tasks.org writes them: `RELATED-TO;RELTYPE=PARENT:<parent UID>` on the child, so
they nest identically on your phone. They show under their parent with tree guides, to any depth, and a parent
shows how many are done: `Walmart (1/2)` (completed subtasks count even while hidden). `A` adds a subtask under
the selected task; `h`/`l`/`z` fold and unfold (remembered between runs; a folded parent shows `▸`).

Deleting a parent never deletes its subtasks; they stay as separate tasks, and the delete prompt says so.
Moving a task to a different parent isn't supported yet.

## Default priority

New tasks get the priority in `default_priority` when you don't give one (`none`, `low`, `medium` or `high`;
unset means none):

    "default_priority": "low"

- quick-add: `buy milk` is low; `buy milk !high` is high; `buy milk !none` really has no priority
- full form: the field is pre-filled with the default; clear it to use the default, or type `none` for none
- only new tasks are affected; existing tasks and edits are never changed
- the `↓` marker is hidden for the default level (it would be on every row); tasks with no priority get a dim
  `–` instead so they stand out, sort below low ones, and say "no priority" in the detail pane

## Quick add

`a` opens a one-line prompt with a live preview; enter adds, tab opens the full form pre-filled.

    call mom fri 5pm !high
    water plants every monday 9am
    pay rent by oct 1 // paid through the app
    plan trip start:oct 5 due:oct 20
    standup weekdays 9:30am

- a date/time at the end of the title is the due date (`tomorrow 2pm`, `fri`, `+3d`, `oct 5`, `in 2 weeks`)
- `!high` `!med` `!low` (or `!1`-`!9`, `!!!`) set priority
- `every week`, `every 2 weeks`, `every other week`, `every mon,wed`, `daily`, `weekdays`... set the repeat;
  with no date, the first matching day becomes the due date
- `due:` / `start:` set dates explicitly (a start date hides the task until then)
- `alarm:10 min before`, `alarm:at due`, `alarm:1 day after due`, `alarm:2h before start` add alarms (repeatable;
  a bare `alarm:10m` means 10 minutes before due)
- everything after ` // ` is the notes; `\word` keeps a word literal (`\!important`)

## Reminders

While the TUI is open it shows native desktop notifications (KDE, GNOME, ... via D-Bus) with **Complete**
and **Postpone** buttons, like the Chrome extension:

- a timed task reminds at its due time; an all-day task reminds at `default_time` (09:00) on its due day
- each reminder fires once, remembered across restarts; a recurring task re-arms when it advances
- reminders you missed while the TUI was closed fire when you open it, if they're less than 12 hours old
- tasks that haven't started yet, or are complete, never remind
- the TUI also syncs in the background every 15 minutes so reminders stay current

Optional config (all keys have defaults):

    "notify": { "enabled": true, "default_time": "09:00", "sync_minutes": 15, "snooze_minutes": 60 }

`./caldav-tui --no-notify` runs without reminders (handy for testing). `./caldav-tui notify-test` pops up a test notification so you can check your desktop and the buttons.

## How sync works

Each calendar is synced incrementally with a WebDAV sync token (RFC 6578): the server says what changed since
the last token, and only those tasks are downloaded. If the token expires the app quietly does one full resync;
if a server has no sync-collection it falls back to diffing ETags. A token only advances together with the data it
describes, so a failed sync never loses changes. `./caldav-tui sync` prints what each sync did.

## Optimistic updates

Writes feel instant: the change appears in the list the moment you make it (with a ⟳ next to the task while it
saves), and is pushed to the server in the background. If the push fails (offline, or the task changed on the
server), the change is rolled back and you get a message, so the list never keeps something the server doesn't have.
A task with a save in flight can't be edited again until it's confirmed.

## How writes work

Every write is a conditional PUT/DELETE (`If-Match` with the task's ETag), built by patching only the
properties the app manages (summary, notes, due, start, priority, repeat, status). Everything else on the
task (categories, other clients' X- properties, absolute-time alarms) is carried through untouched. If the server copy
changed since the last sync you get a "changed on the server" message and an automatic refresh instead of
a lost update.

Alarms show in the detail line and have an **Alarm** field in the edit form: a comma-separated list such as
`at due, 1 day after due` or `10 min before start` (`none` clears them). Saving rewrites only the relative alarms;
ones you leave alone are kept byte-for-byte, and absolute-time alarms are never touched. Only timing is stored on
the server: clients such as Tasks.org keep the alert mode (once / repeating / insistent) locally, so set that
on the phone.

## Status

Read, complete/reopen, add, subtask, edit, delete and reminders work. Next (only if needed): a persistent offline write queue, so changes made with no network survive and are
sent later instead of being rolled back.
