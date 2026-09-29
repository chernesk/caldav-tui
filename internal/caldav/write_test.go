package caldav

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

// fakeServer is a tiny in-memory CalDAV object store with ETag preconditions.
type fakeServer struct {
	mu   sync.Mutex
	objs map[string]string
	tags map[string]int
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, p, ok := r.BasicAuth(); !ok || u != "me" || p != "pw" {
		w.WriteHeader(401)
		return
	}
	cur, exists := f.objs[r.URL.Path]
	etag := `"` + string(rune('a'+f.tags[r.URL.Path])) + `"`
	switch r.Method {
	case "PUT":
		if r.Header.Get("If-None-Match") == "*" && exists {
			w.WriteHeader(412)
			return
		}
		if m := r.Header.Get("If-Match"); m != "" && (!exists || m != etag) {
			w.WriteHeader(412)
			return
		}
		b, _ := io.ReadAll(r.Body)
		f.objs[r.URL.Path] = string(b)
		f.tags[r.URL.Path]++
		w.Header().Set("ETag", `"`+string(rune('a'+f.tags[r.URL.Path]))+`"`)
		w.WriteHeader(201)
	case "DELETE":
		if !exists {
			w.WriteHeader(404)
			return
		}
		if m := r.Header.Get("If-Match"); m != "" && m != etag {
			w.WriteHeader(412)
			return
		}
		delete(f.objs, r.URL.Path)
		w.WriteHeader(204)
	case "HEAD":
		if !exists {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("ETag", etag)
	default:
		_ = cur
		w.WriteHeader(405)
	}
}

func newTestClient(t *testing.T) (*Client, *fakeServer) {
	f := &fakeServer{objs: map[string]string{}, tags: map[string]int{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return &Client{base: u, user: "me", pass: "pw", hc: srv.Client()}, f
}

func TestPutCreateUpdateDelete(t *testing.T) {
	cl, f := newTestClient(t)
	ctx := context.Background()
	href := "/cal/x.ics"

	etag, err := cl.Put(ctx, href, "v1", "")
	if err != nil || etag != `"b"` {
		t.Fatalf("create: etag=%q err=%v", etag, err)
	}
	if _, err := cl.Put(ctx, href, "dup", ""); !errors.Is(err, ErrConflict) {
		t.Errorf("create over existing: %v", err)
	}
	etag2, err := cl.Put(ctx, href, "v2", etag)
	if err != nil || f.objs[href] != "v2" {
		t.Fatalf("update: %v %q", err, f.objs[href])
	}
	if _, err := cl.Put(ctx, href, "v3", etag); !errors.Is(err, ErrConflict) {
		t.Errorf("stale etag should conflict, got %v", err)
	}
	if f.objs[href] != "v2" {
		t.Errorf("stale write clobbered data: %q", f.objs[href])
	}
	if err := cl.Delete(ctx, href, etag); !errors.Is(err, ErrConflict) {
		t.Errorf("stale delete: %v", err)
	}
	if err := cl.Delete(ctx, href, etag2); err != nil {
		t.Errorf("delete: %v", err)
	}
	if err := cl.Delete(ctx, href, ""); err != nil {
		t.Errorf("deleting missing should be ok: %v", err)
	}
}

func TestAuthFailure(t *testing.T) {
	cl, _ := newTestClient(t)
	cl.pass = "wrong"
	_, err := cl.Put(context.Background(), "/a.ics", "x", "")
	if err == nil || errors.Is(err, ErrConflict) {
		t.Errorf("want auth error, got %v", err)
	}
}
