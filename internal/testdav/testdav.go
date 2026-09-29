// Package testdav is an in-memory stand-in for the write half of a CalDAV
// server (PUT/DELETE/HEAD with ETag preconditions), for tests.
package testdav

import (
	"io"
	"net/http"
	"sync"
)

type Server struct {
	mu   sync.Mutex
	objs map[string]string
	gen  map[string]int
}

func New() *Server { return &Server{objs: map[string]string{}, gen: map[string]int{}} }

func (f *Server) etag(p string) string { return `"` + string(rune('a'+f.gen[p])) + `"` }

// Get returns the stored body at a path ("" if absent).
func (f *Server) Get(path string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.objs[path]
}

// Gen returns how many writes a path has seen.
func (f *Server) Gen(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gen[path]
}

// Bump changes a path's ETag, simulating an edit from another client.
func (f *Server) Bump(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gen[path]++
}

func (f *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, exists := f.objs[r.URL.Path]
	switch r.Method {
	case "PUT":
		if r.Header.Get("If-None-Match") == "*" && exists {
			w.WriteHeader(412)
			return
		}
		if m := r.Header.Get("If-Match"); m != "" && (!exists || m != f.etag(r.URL.Path)) {
			w.WriteHeader(412)
			return
		}
		b, _ := io.ReadAll(r.Body)
		f.objs[r.URL.Path] = string(b)
		f.gen[r.URL.Path]++
		w.Header().Set("ETag", f.etag(r.URL.Path))
		w.WriteHeader(201)
	case "DELETE":
		if !exists {
			w.WriteHeader(404)
			return
		}
		if m := r.Header.Get("If-Match"); m != "" && m != f.etag(r.URL.Path) {
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
		w.Header().Set("ETag", f.etag(r.URL.Path))
	default:
		w.WriteHeader(405)
	}
}
