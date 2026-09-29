package caldav

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/emersion/go-ical"
)

// ErrConflict means the server's copy changed since we cached it (HTTP 412),
// or the object we expected to exist is gone. Callers should re-sync.
var ErrConflict = errors.New("task changed on the server")

func (cl *Client) do(ctx context.Context, method, href string, body string, hdr map[string]string) (*http.Response, error) {
	u := cl.base.ResolveReference(&url.URL{Path: href})
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(cl.user, cl.pass)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return cl.hc.Do(req)
}

func statusErr(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	switch resp.StatusCode {
	case http.StatusPreconditionFailed, http.StatusNotFound, http.StatusConflict:
		return fmt.Errorf("%w (HTTP %d)", ErrConflict, resp.StatusCode)
	case http.StatusUnauthorized:
		return errors.New("authentication failed (HTTP 401)")
	}
	return fmt.Errorf("server returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
}

// Put writes a calendar object. With etag set it only overwrites that exact
// version (If-Match); with etag empty it only creates (If-None-Match: *). It
// returns the stored object's ETag, fetching it when the server omits the header.
func (cl *Client) Put(ctx context.Context, href, raw, etag string) (string, error) {
	hdr := map[string]string{"Content-Type": "text/calendar; charset=utf-8"}
	if etag != "" {
		hdr["If-Match"] = etag
	} else {
		hdr["If-None-Match"] = "*"
	}
	resp, err := cl.do(ctx, http.MethodPut, href, raw, hdr)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", statusErr(resp)
	}
	if e := resp.Header.Get("ETag"); e != "" {
		return e, nil
	}
	return cl.ETag(ctx, href)
}

// ETag fetches the current ETag of an object ("" if unavailable).
func (cl *Client) ETag(ctx context.Context, href string) (string, error) {
	resp, err := cl.do(ctx, http.MethodHead, href, "", nil)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", nil // not fatal: the next sync fills it in and a stale etag just 412s
	}
	return resp.Header.Get("ETag"), nil
}

// Delete removes a calendar object, guarded by its ETag when known.
func (cl *Client) Delete(ctx context.Context, href, etag string) error {
	hdr := map[string]string{}
	if etag != "" {
		hdr["If-Match"] = etag
	}
	resp, err := cl.do(ctx, http.MethodDelete, href, "", hdr)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 2 || resp.StatusCode == http.StatusNotFound {
		return nil // already gone is fine
	}
	return statusErr(resp)
}

// TaskFromRaw parses stored/serialized iCalendar text into a Task.
func TaskFromRaw(href, etag, calendar, raw string) (Task, error) {
	cal, err := ical.NewDecoder(strings.NewReader(raw)).Decode()
	if err != nil {
		return Task{}, err
	}
	t, ok := ParseTask(href, etag, calendar, cal)
	if !ok {
		return Task{}, errors.New("no VTODO in object")
	}
	return t, nil
}
