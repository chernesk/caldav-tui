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

## Keys

List: j/k move · ctrl-d/u half page · g/G top/bottom · tab/shift-tab calendar · c show completed ·
s show unstarted (start date in the future; hidden by default) · r refresh · q quit

Actions: x or space complete/reopen (recurring tasks advance to the next occurrence) · a add ·
A add subtask under the selected task · e or enter edit · d delete (asks y/N)

Form: tab/shift-tab or up/down between fields · enter or ctrl-s save · esc cancel. Dates take
`today`, `tomorrow 2pm`, `fri`, `+3d`, `oct 5`, `2026-10-05 14:30`; repeat takes `daily`,
`weekly mon,wed`, `every 2 weeks`, `monthly`, `weekdays`, or a raw `FREQ=...` rule.

## How writes work

Every write is a conditional PUT/DELETE (`If-Match` with the task's ETag), built by patching only the
properties the app manages (summary, notes, due, start, priority, repeat, status). Everything else on the
task (alarms, categories, other clients' X- properties) is carried through untouched. If the server copy
changed since the last sync you get a "changed on the server" message and an automatic refresh instead of
a lost update.

## Status

Read, complete/reopen, add, subtask, edit and delete work. Next: quick-add with natural language in
the title, sync-token incremental sync, offline write queue.
