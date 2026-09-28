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

j/k move · ctrl-d/u half page · g/G top/bottom · tab/shift-tab calendar · c show completed · r refresh · q quit

## Status

Step 1: read-only list with cache + sync, subtask tree, detail pane.
Next: complete/add/edit/delete (If-Match ETag PUTs, patching only managed properties), recurrence, sync-token.
