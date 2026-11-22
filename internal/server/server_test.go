package server

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fake struct {
	calls []string
	err   error
}

func (f *fake) Presign(_ context.Context, method, key string, now time.Time) (string, error) {
	f.calls = append(f.calls, method+" "+key)
	if f.err != nil {
		return "", f.err
	}
	return "https://example.invalid/" + key + "?X-Amz-Signature=SIGNED-" + method, nil
}

var now = time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)

func newServer(groups ...Group) (*Server, *bytes.Buffer) {
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	return New(groups, log, func() time.Time { return now }), &logs
}

func do(s *Server, method, target string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(method, target, nil))
	return w
}

func TestRedirect(t *testing.T) {
	browser, desktop := &fake{}, &fake{}
	s, logs := newServer(Group{"lanescape/browser", browser}, Group{"lanescape/desktop", desktop})
	for _, method := range []string{"GET", "HEAD"} {
		w := do(s, method, "/lanescape/browser/v1/20261122.1/lanescape-browser-linux-amd64.tar.gz?ignored=1")
		if w.Code != http.StatusTemporaryRedirect {
			t.Fatalf("%s: %d", method, w.Code)
		}
		want := "https://example.invalid/v1/20261122.1/lanescape-browser-linux-amd64.tar.gz?X-Amz-Signature=SIGNED-" + method
		if got := w.Header().Get("Location"); got != want {
			t.Errorf("%s: Location %q", method, got)
		}
		if got := w.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control %q", method, got)
		}
		if w.Body.Len() != 0 {
			t.Errorf("%s: body %q", method, w.Body.String())
		}
	}
	if w := do(s, "GET", "/lanescape/desktop/v1/latest"); w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("desktop: %d", w.Code)
	}
	if strings.Join(browser.calls, ",") != "GET v1/20261122.1/lanescape-browser-linux-amd64.tar.gz,HEAD v1/20261122.1/lanescape-browser-linux-amd64.tar.gz" {
		t.Errorf("browser's calls %v", browser.calls)
	}
	if strings.Join(desktop.calls, ",") != "GET v1/latest" {
		t.Errorf("desktop's calls %v", desktop.calls)
	}
	// The log has the group and key, never the URL or its signature.
	if strings.Contains(logs.String(), "SIGNED") || strings.Contains(logs.String(), "example.invalid") {
		t.Fatalf("the log holds the redirect: %s", logs.String())
	}
	if !strings.Contains(logs.String(), `"group":"lanescape/browser"`) || !strings.Contains(logs.String(), `"key":"v1/latest"`) {
		t.Errorf("logs: %s", logs.String())
	}
}

func TestRefused(t *testing.T) {
	p := &fake{}
	s, _ := newServer(Group{"lanescape/browser", p})
	for _, tc := range []struct {
		method, target string
		status         int
	}{
		{"POST", "/lanescape/browser/v1/latest", http.StatusMethodNotAllowed},
		{"PUT", "/lanescape/browser/v1/latest", http.StatusMethodNotAllowed},
		{"DELETE", "/lanescape/browser/v1/latest", http.StatusMethodNotAllowed},
		{"GET", "/", http.StatusNotFound},
		{"GET", "/lanescape", http.StatusNotFound},
		{"GET", "/lanescape/browser", http.StatusNotFound},
		{"GET", "/lanescape/browser/", http.StatusNotFound},
		{"GET", "/lanescape/browserx/v1/latest", http.StatusNotFound},
		{"GET", "/lanescape/other/v1/latest", http.StatusNotFound},
		{"GET", "/lanescape/browser/v1//latest", http.StatusNotFound},
		{"GET", "/lanescape/browser/v1/latest/", http.StatusNotFound},
		{"GET", "/lanescape/browser/../browser/v1/latest", http.StatusNotFound},
		{"GET", "/lanescape/browser/v1/./latest", http.StatusNotFound},
		{"GET", "/lanescape/browser/v1/%2e%2e/x", http.StatusNotFound},
		{"GET", "/lanescape/browser/v1%2Flatest", http.StatusNotFound},
		{"GET", "/lanescape%2Fbrowser/v1/latest", http.StatusNotFound},
		{"GET", "/lanescape/browser/a%20b", http.StatusNotFound},
		{"GET", "/lanescape/browser/a+b", http.StatusNotFound},
		{"GET", "/lanescape/browser/" + strings.Repeat("a", 1025), http.StatusNotFound},
	} {
		w := do(s, tc.method, tc.target)
		if w.Code != tc.status {
			t.Errorf("%s %s: %d, want %d", tc.method, tc.target, w.Code, tc.status)
		}
		if w.Header().Get("Location") != "" {
			t.Errorf("%s %s: redirected", tc.method, tc.target)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s %s: Cache-Control %q", tc.method, tc.target, w.Header().Get("Cache-Control"))
		}
		if tc.status == http.StatusMethodNotAllowed && w.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("%s %s: Allow %q", tc.method, tc.target, w.Header().Get("Allow"))
		}
	}
	if len(p.calls) != 0 {
		t.Errorf("presigned %v", p.calls)
	}
	// The longest key is served.
	if w := do(s, "GET", "/lanescape/browser/"+strings.Repeat("a", 1024)); w.Code != http.StatusTemporaryRedirect {
		t.Errorf("a 1024-byte key: %d", w.Code)
	}
}

func TestPresignFails(t *testing.T) {
	s, logs := newServer(Group{"lanescape/browser", &fake{err: errors.New("broken")}})
	w := do(s, "GET", "/lanescape/browser/v1/latest")
	if w.Code != http.StatusInternalServerError || w.Header().Get("Location") != "" {
		t.Fatalf("%d %q", w.Code, w.Header().Get("Location"))
	}
	if !strings.Contains(logs.String(), "presign failed") {
		t.Errorf("logs: %s", logs.String())
	}
}
