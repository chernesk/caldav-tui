package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/chernesk/caldav-tui/internal/caldav"
	"github.com/chernesk/caldav-tui/internal/config"
	"github.com/chernesk/caldav-tui/internal/notify"
	"github.com/chernesk/caldav-tui/internal/store"
	"github.com/chernesk/caldav-tui/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// notifyTest pops up one notification with the reminder buttons and waits for a click.
func notifyTest() error {
	n, err := notify.New()
	if err != nil {
		return fmt.Errorf("no desktop notification service: %w", err)
	}
	defer n.Shutdown()
	id, err := n.Send("caldav-tui test", "If you can see this, reminders will work. Try a button.",
		[]notify.Action{{Key: "complete", Label: "Complete"}, {Key: "snooze", Label: "Postpone 1h"}})
	if err != nil {
		return err
	}
	fmt.Printf("sent notification %d; waiting 20s for a click...\n", id)
	timeout := time.After(20 * time.Second)
	for {
		select {
		case ev := <-n.Events():
			if ev.ID != id {
				continue
			}
			if ev.Closed {
				fmt.Println("notification closed/dismissed")
				return nil
			}
			fmt.Printf("button clicked: %s\n", ev.Action)
			n.Close(id)
			return nil
		case <-timeout:
			n.Close(id)
			fmt.Println("no click (sending worked; buttons untested)")
			return nil
		}
	}
}

func run() error {
	if len(os.Args) > 1 && os.Args[1] == "notify-test" {
		return notifyTest()
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pass, err := cfg.ResolvePassword()
	if err != nil {
		return err
	}
	cl, err := caldav.New(cfg.Server, cfg.Username, pass)
	if err != nil {
		return err
	}
	dbPath, err := config.CachePath()
	if err != nil {
		return err
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	// `caldav-tui sync` does one sync and prints a summary; handy for debugging.
	if len(os.Args) > 1 && os.Args[1] == "sync" {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := tui.Sync(ctx, cl, st); err != nil {
			return err
		}
		cals, _ := st.Calendars()
		for _, c := range cals {
			ts, _ := st.Tasks(c.Path)
			fmt.Printf("%-30s %d tasks\n", c.Name, len(ts))
		}
		return nil
	}

	model := tui.New(cl, st)
	if cfg.Notify.On() && !slices.Contains(os.Args[1:], "--no-notify") {
		if n, err := notify.New(); err != nil {
			fmt.Fprintf(os.Stderr, "reminders off: no desktop notification service (%v)\n", err)
		} else {
			defer n.Shutdown()
			clock, _ := time.Parse("15:04", cfg.Notify.DefaultTime)
			model = model.WithReminders(n, tui.ReminderConfig{
				DefaultClock: time.Duration(clock.Hour())*time.Hour + time.Duration(clock.Minute())*time.Minute,
				SyncEvery:    time.Duration(cfg.Notify.SyncMinutes) * time.Minute,
				Snooze:       time.Duration(cfg.Notify.SnoozeMinutes) * time.Minute,
			})
		}
	}
	_, err = tea.NewProgram(model, tea.WithAltScreen()).Run()
	return err
}
