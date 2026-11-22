package app

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lanescape/downloads/config"
	r2emu "github.com/lanescape/downloads/emulators/r2"
	"github.com/lanescape/downloads/internal/secrets"
)

// The service against the R2 emulator, end to end: a client asks the
// service, follows its redirect to the emulator, and gets the object.

const testConfig = `
url_lifetime: 10m
groups:
  lanescape/browser:
    r2:
      account_id: 0123456789abcdef0123456789abcdef
      bucket: lanescape-browser
      access_key_id_secret_name: lanescape-downloads-test-browser-id
      secret_access_key_secret_name: lanescape-downloads-test-browser-secret
  lanescape/desktop:
    r2:
      account_id: 0123456789abcdef0123456789abcdef
      bucket: lanescape-desktop
      access_key_id_secret_name: lanescape-downloads-test-desktop-id
      secret_access_key_secret_name: lanescape-downloads-test-desktop-secret
`

type world struct {
	emu     *r2emu.Emulator
	service *httptest.Server
	logs    *bytes.Buffer
	now     time.Time
}

func secretsFile(t *testing.T, m map[string]string) secrets.Source {
	t.Helper()
	var b strings.Builder
	for k, v := range m {
		b.WriteString(k + ": " + v + "\n")
	}
	path := filepath.Join(t.TempDir(), "secrets.yaml")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := secrets.File(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func start(t *testing.T, browserSecret string) *world {
	t.Helper()
	w := &world{emu: r2emu.New(), logs: &bytes.Buffer{}, now: time.Now().UTC()}
	w.emu.Now = func() time.Time { return w.now }
	w.emu.AddBucket("lanescape-browser")
	w.emu.AddBucket("lanescape-desktop")
	// Read-only tokens, each for its own bucket, as the service's are.
	w.emu.AddToken("browser-id", "browser-secret", r2emu.Read, "lanescape-browser")
	w.emu.AddToken("desktop-id", "desktop-secret", r2emu.Read, "lanescape-desktop")
	emu := httptest.NewServer(w.emu)
	t.Cleanup(emu.Close)

	c, err := config.Parse([]byte(testConfig))
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(c, Options{
		Secrets: secretsFile(t, map[string]string{
			"lanescape-downloads-test-browser-id":     "browser-id",
			"lanescape-downloads-test-browser-secret": browserSecret,
			"lanescape-downloads-test-desktop-id":     "desktop-id",
			"lanescape-downloads-test-desktop-secret": "desktop-secret",
		}),
		R2Endpoint: emu.URL,
		Log:        slog.New(slog.NewJSONHandler(w.logs, nil)),
		Now:        func() time.Time { return w.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	w.service = httptest.NewServer(h)
	t.Cleanup(w.service.Close)
	return w
}

func fetch(t *testing.T, method, url string) (*http.Response, []byte) {
	t.Helper()
	r, _ := http.NewRequest(method, url, nil)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestDownload(t *testing.T) {
	w := start(t, "browser-secret")
	pkg := bytes.Repeat([]byte("package "), 4096)
	w.emu.Put("lanescape-browser", "v1/20261122.1/lanescape-browser-linux-amd64.tar.gz", pkg, "application/gzip")
	w.emu.Put("lanescape-browser", "v1/latest", []byte("20261122.1\n"), "text/plain")
	w.emu.Put("lanescape-desktop", "v1/latest", []byte("desktop\n"), "text/plain")

	resp, b := fetch(t, "GET", w.service.URL+"/lanescape/browser/v1/20261122.1/lanescape-browser-linux-amd64.tar.gz")
	if resp.StatusCode != 200 || !bytes.Equal(b, pkg) {
		t.Fatalf("package: %d, %d bytes", resp.StatusCode, len(b))
	}
	// The client was redirected once, to the emulator.
	if resp.Request.URL.Host == strings.TrimPrefix(w.service.URL, "http://") {
		t.Fatal("not redirected")
	}
	if _, b := fetch(t, "GET", w.service.URL+"/lanescape/browser/v1/latest"); string(b) != "20261122.1\n" {
		t.Fatalf("latest %q", b)
	}
	if _, b := fetch(t, "GET", w.service.URL+"/lanescape/desktop/v1/latest"); string(b) != "desktop\n" {
		t.Fatalf("desktop's latest %q", b)
	}
	resp, b = fetch(t, "HEAD", w.service.URL+"/lanescape/browser/v1/latest")
	if resp.StatusCode != 200 || len(b) != 0 || resp.Header.Get("Content-Length") != "11" {
		t.Fatalf("HEAD: %d %v", resp.StatusCode, resp.Header)
	}
	// An object that doesn't exist is the provider's 404.
	resp, b = fetch(t, "GET", w.service.URL+"/lanescape/browser/v1/none")
	if resp.StatusCode != 404 || !strings.Contains(string(b), "NoSuchKey") {
		t.Fatalf("none: %d %s", resp.StatusCode, b)
	}
	// The log never holds a URL the service signed.
	if strings.Contains(w.logs.String(), "X-Amz") || strings.Contains(w.logs.String(), "127.0.0.1") {
		t.Fatalf("logs hold a signed URL: %s", w.logs.String())
	}
}

func TestWrongSecret(t *testing.T) {
	w := start(t, "not-the-secret")
	w.emu.Put("lanescape-browser", "v1/latest", []byte("x"), "text/plain")
	resp, b := fetch(t, "GET", w.service.URL+"/lanescape/browser/v1/latest")
	if resp.StatusCode != 403 || !strings.Contains(string(b), "SignatureDoesNotMatch") {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
}

func TestExpired(t *testing.T) {
	w := start(t, "browser-secret")
	w.emu.Put("lanescape-browser", "v1/latest", []byte("x"), "text/plain")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(w.service.URL + "/lanescape/browser/v1/latest")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("%d", resp.StatusCode)
	}
	location := resp.Header.Get("Location")
	// The configuration's lifetime, 10 minutes, then a second.
	w.now = w.now.Add(10*time.Minute + time.Second)
	resp, b := fetch(t, "GET", location)
	if resp.StatusCode != 403 || !strings.Contains(string(b), "<Code>ExpiredRequest</Code>") {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
}

func TestMissingSecret(t *testing.T) {
	c, err := config.Parse([]byte(testConfig))
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(c, Options{
		Secrets: secretsFile(t, map[string]string{"lanescape-downloads-test-browser-id": "x"}),
		Log:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if !errors.Is(err, secrets.ErrMissing) || !strings.Contains(err.Error(), "lanescape-downloads-test-browser-secret") {
		t.Fatalf("err = %v", err)
	}
}
