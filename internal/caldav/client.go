// Package caldav wraps go-webdav for discovering task calendars and fetching VTODOs.
package caldav

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	wcaldav "github.com/emersion/go-webdav/caldav"
)

type Client struct {
	c *wcaldav.Client
}

type Calendar struct {
	Path string
	Name string
}

// resolveEndpoint applies RFC 6764 bootstrapping: when the configured server URL
// has no path, follow /.well-known/caldav ourselves. Go's HTTP client rewrites a
// redirected PROPFIND into a GET, so it can't be left to follow the 301.
func resolveEndpoint(server string) string {
	u, err := url.Parse(server)
	if err != nil || strings.Trim(u.Path, "/") != "" {
		return server
	}
	wk := u.ResolveReference(&url.URL{Path: "/.well-known/caldav"})
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequest(http.MethodHead, wk.String(), nil)
	if err != nil {
		return server
	}
	resp, err := hc.Do(req)
	if err != nil {
		return server
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		if loc, err := resp.Location(); err == nil {
			return loc.String()
		}
	}
	if resp.StatusCode < 300 {
		return wk.String()
	}
	return server
}

func New(server, user, pass string) (*Client, error) {
	hc := webdav.HTTPClientWithBasicAuth(http.DefaultClient, user, pass)
	c, err := wcaldav.NewClient(hc, resolveEndpoint(server))
	if err != nil {
		return nil, err
	}
	return &Client{c: c}, nil
}

// Calendars discovers all calendars that support VTODO.
func (cl *Client) Calendars(ctx context.Context) ([]Calendar, error) {
	principal, err := cl.c.FindCurrentUserPrincipal(ctx)
	if err != nil {
		return nil, fmt.Errorf("discover principal: %w", err)
	}
	home, err := cl.c.FindCalendarHomeSet(ctx, principal)
	if err != nil {
		return nil, fmt.Errorf("discover calendar home: %w", err)
	}
	cals, err := cl.c.FindCalendars(ctx, home)
	if err != nil {
		return nil, fmt.Errorf("list calendars: %w", err)
	}
	var out []Calendar
	for _, c := range cals {
		if slices.Contains(c.SupportedComponentSet, ical.CompToDo) {
			out = append(out, Calendar{Path: c.Path, Name: c.Name})
		}
	}
	return out, nil
}

// Tasks fetches every VTODO (including completed) in a calendar.
func (cl *Client) Tasks(ctx context.Context, cal Calendar) ([]Task, error) {
	objs, err := cl.c.QueryCalendar(ctx, cal.Path, &wcaldav.CalendarQuery{
		CompRequest: wcaldav.CalendarCompRequest{Name: ical.CompCalendar, AllProps: true, AllComps: true},
		CompFilter:  wcaldav.CompFilter{Name: ical.CompCalendar, Comps: []wcaldav.CompFilter{{Name: ical.CompToDo}}},
	})
	if err != nil {
		return nil, fmt.Errorf("query %s: %w", cal.Name, err)
	}
	var out []Task
	for _, o := range objs {
		if o.Data == nil {
			continue
		}
		if t, ok := ParseTask(o.Path, o.ETag, cal.Path, o.Data); ok {
			out = append(out, t)
		}
	}
	return out, nil
}
