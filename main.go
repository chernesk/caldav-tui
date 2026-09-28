package main

import (
	"context"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/chernesk/caldav-tui/internal/caldav"
	"github.com/chernesk/caldav-tui/internal/config"
	"github.com/chernesk/caldav-tui/internal/store"
	"github.com/chernesk/caldav-tui/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
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

	_, err = tea.NewProgram(tui.New(cl, st), tea.WithAltScreen()).Run()
	return err
}
