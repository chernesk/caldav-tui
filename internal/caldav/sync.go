package caldav

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/emersion/go-ical"
	wcaldav "github.com/emersion/go-webdav/caldav"
)

var (
	// ErrSyncTokenInvalid means the server no longer honors our sync token
	// (RFC 6578 valid-sync-token). Drop the token and resync from scratch.
	ErrSyncTokenInvalid = errors.New("sync token invalid")
	// ErrSyncUnsupported means the server can't do sync-collection.
	ErrSyncUnsupported = errors.New("server does not support sync-collection")
)

// Change is a calendar object and its current ETag.
type Change struct{ Href, ETag string }

// Delta is what changed in a calendar since a sync token.
type Delta struct {
	Token   string
	Changed []Change // added or modified
	Deleted []string // hrefs
}

type multistatus struct {
	Responses []struct {
		Href     string `xml:"href"`
		Status   string `xml:"status"`
		Propstat []struct {
			Status string `xml:"status"`
			Prop   struct {
				ETag string `xml:"getetag"`
			} `xml:"prop"`
		} `xml:"propstat"`
	} `xml:"response"`
	SyncToken string `xml:"sync-token"`
}

func (cl *Client) report(ctx context.Context, href, body string, depth string) (*http.Response, []byte, error) {
	resp, err := cl.do(ctx, "REPORT", href, body, map[string]string{
		"Content-Type": "application/xml; charset=utf-8",
		"Depth":        depth,
	})
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	b, err := readAll(resp)
	return resp, b, err
}

func normPath(href string) string {
	if u, err := url.Parse(strings.TrimSpace(href)); err == nil {
		href = u.Path
	}
	return href
}

func sameCollection(a, b string) bool {
	return strings.TrimRight(normPath(a), "/") == strings.TrimRight(normPath(b), "/")
}

// parseMultistatus splits a multistatus into live (200) and deleted (404) objects.
func parseMultistatus(body []byte, calPath string) (*Delta, error) {
	var ms multistatus
	if err := xml.Unmarshal(body, &ms); err != nil {
		return nil, fmt.Errorf("parse multistatus: %w", err)
	}
	d := &Delta{Token: strings.TrimSpace(ms.SyncToken)}
	for _, r := range ms.Responses {
		p := normPath(r.Href)
		if p == "" || sameCollection(p, calPath) {
			continue
		}
		if strings.Contains(r.Status, " 404") {
			d.Deleted = append(d.Deleted, p)
			continue
		}
		etag := ""
		for _, ps := range r.Propstat {
			if strings.Contains(ps.Status, " 200") && ps.Prop.ETag != "" {
				etag = NormETag(ps.Prop.ETag)
			}
		}
		d.Changed = append(d.Changed, Change{Href: p, ETag: etag})
	}
	return d, nil
}

// SyncCollection asks the server what changed in a calendar since token
// (RFC 6578). An empty token returns every object plus a first token.
func (cl *Client) SyncCollection(ctx context.Context, calPath, token string) (*Delta, error) {
	body := `<?xml version="1.0" encoding="utf-8"?>
<D:sync-collection xmlns:D="DAV:">
  <D:sync-token>` + xmlEscape(token) + `</D:sync-token>
  <D:sync-level>1</D:sync-level>
  <D:prop><D:getetag/></D:prop>
</D:sync-collection>`
	resp, b, err := cl.report(ctx, calPath, body, "0")
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusMultiStatus:
	case strings.Contains(string(b), "valid-sync-token"),
		resp.StatusCode == http.StatusForbidden && token != "",
		resp.StatusCode == http.StatusConflict && token != "":
		return nil, ErrSyncTokenInvalid
	case resp.StatusCode == http.StatusNotImplemented, resp.StatusCode == http.StatusMethodNotAllowed,
		resp.StatusCode == http.StatusBadRequest, resp.StatusCode == http.StatusForbidden, resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w (HTTP %d)", ErrSyncUnsupported, resp.StatusCode)
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, errors.New("authentication failed (HTTP 401)")
	default:
		return nil, fmt.Errorf("sync-collection: HTTP %d", resp.StatusCode)
	}
	d, err := parseMultistatus(b, calPath)
	if err != nil {
		return nil, err
	}
	if d.Token == "" {
		return nil, fmt.Errorf("%w (no sync-token in response)", ErrSyncUnsupported)
	}
	return d, nil
}

// ListETags returns every VTODO's href and ETag without downloading the data.
// It's the fallback for servers without sync-collection: diff against the cache.
func (cl *Client) ListETags(ctx context.Context, calPath string) ([]Change, error) {
	body := `<?xml version="1.0" encoding="utf-8"?>
<C:calendar-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:prop><D:getetag/></D:prop>
  <C:filter><C:comp-filter name="VCALENDAR"><C:comp-filter name="VTODO"/></C:comp-filter></C:filter>
</C:calendar-query>`
	resp, b, err := cl.report(ctx, calPath, body, "1")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusMultiStatus {
		return nil, fmt.Errorf("list etags: HTTP %d", resp.StatusCode)
	}
	d, err := parseMultistatus(b, calPath)
	if err != nil {
		return nil, err
	}
	return d.Changed, nil
}

// FetchTasks downloads specific objects (calendar-multiget) in batches.
func (cl *Client) FetchTasks(ctx context.Context, cal Calendar, hrefs []string) ([]Task, error) {
	const batch = 50
	var out []Task
	for i := 0; i < len(hrefs); i += batch {
		chunk := hrefs[i:min(i+batch, len(hrefs))]
		objs, err := cl.c.MultiGetCalendar(ctx, cal.Path, &wcaldav.CalendarMultiGet{
			Paths:       chunk,
			CompRequest: wcaldav.CalendarCompRequest{Name: ical.CompCalendar, AllProps: true, AllComps: true},
		})
		if err != nil {
			return nil, fmt.Errorf("fetch %s: %w", cal.Name, err)
		}
		for _, o := range objs {
			if o.Data == nil {
				continue
			}
			if t, ok := ParseTask(o.Path, o.ETag, cal.Path, o.Data); ok {
				out = append(out, t)
			}
		}
	}
	return out, nil
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
