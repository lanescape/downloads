// Package server answers download requests (specs/0001-service.md,
// Requests): GET or HEAD of /<group>/<key> is a temporary redirect to the
// object key at the group's provider.
//
// The redirect's Location is a presigned URL, a credential for its object
// until it expires, so it's never logged; nor is anything else a provider
// signs with.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Provider gives a URL a client may fetch an object from for a while.
type Provider interface {
	Presign(ctx context.Context, method, key string, now time.Time) (string, error)
}

// Group is a group's path and its provider.
type Group struct {
	Path     string
	Provider Provider
}

// Server serves the groups.
type Server struct {
	groups []Group
	log    *slog.Logger
	now    func() time.Time
}

// New is a server of groups. No group's path may be within another's
// (config.Parse checks).
func New(groups []Group, log *slog.Logger, now func() time.Time) *Server {
	return &Server{groups: groups, log: log, now: now}
}

// maxKey is the longest key served: S3's limit on a key, in bytes.
const maxKey = 1024

// A key's segments: what an object's name needs, and nothing a URL would
// have to escape, so the path a client asked for is exactly the object's
// key, signed as it is.
var keySegment = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// ServeHTTP answers a request.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	// Nothing here may be cached: a group's newest build changes behind the
	// same path, and a redirect expires with its URL.
	h.Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		h.Set("Allow", "GET, HEAD")
		s.answer(w, r, http.StatusMethodNotAllowed, "", "")
		return
	}
	// The path as the client sent it: an escaped character is refused,
	// rather than decoded into something else.
	path, ok := strings.CutPrefix(r.URL.EscapedPath(), "/")
	if !ok {
		s.answer(w, r, http.StatusNotFound, "", "")
		return
	}
	g, key, ok := s.find(path)
	if !ok {
		s.answer(w, r, http.StatusNotFound, "", "")
		return
	}
	if !validKey(key) {
		s.answer(w, r, http.StatusNotFound, g.Path, "")
		return
	}
	u, err := g.Provider.Presign(r.Context(), r.Method, key, s.now())
	if err != nil {
		s.log.Error("presign failed", "group", g.Path, "key", key, "err", err)
		s.answer(w, r, http.StatusInternalServerError, g.Path, key)
		return
	}
	h.Set("Location", u)
	s.answer(w, r, http.StatusTemporaryRedirect, g.Path, key)
}

func (s *Server) find(path string) (*Group, string, bool) {
	for i := range s.groups {
		g := &s.groups[i]
		if key, ok := strings.CutPrefix(path, g.Path+"/"); ok {
			return g, key, true
		}
	}
	return nil, "", false
}

func validKey(key string) bool {
	if key == "" || len(key) > maxKey {
		return false
	}
	for seg := range strings.SplitSeq(key, "/") {
		if seg == "." || seg == ".." || !keySegment.MatchString(seg) {
			return false
		}
	}
	return true
}

// answer writes the status, with a short line for a GET that isn't
// redirected, and logs the request. The log never has the Location.
func (s *Server) answer(w http.ResponseWriter, r *http.Request, status int, group, key string) {
	if status != http.StatusTemporaryRedirect {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.WriteHeader(status)
	if r.Method == http.MethodGet && status != http.StatusTemporaryRedirect {
		_, _ = w.Write([]byte(http.StatusText(status) + "\n"))
	}
	attrs := []any{"method", r.Method, "status", status}
	if group != "" {
		attrs = append(attrs, "group", group)
	}
	if key != "" {
		attrs = append(attrs, "key", key)
	}
	if key == "" {
		// What was asked for, briefly, since it named no object served.
		attrs = append(attrs, "path", truncate(r.URL.EscapedPath(), 256))
	}
	s.log.Info("request", attrs...)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
