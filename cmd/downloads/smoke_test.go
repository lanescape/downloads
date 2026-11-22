package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lanescape/downloads/config"
	r2emu "github.com/lanescape/downloads/emulators/r2"
	"github.com/lanescape/downloads/internal/app"
	"github.com/lanescape/downloads/internal/secrets"
)

// The smoke check against the service and the R2 emulator.
func smokeWorld(t *testing.T, secret string) (*config.Config, string, string) {
	t.Helper()
	emu := r2emu.New()
	emu.AddBucket("bucket-one")
	emu.AddToken("id", "secret", r2emu.Read, "bucket-one")
	es := httptest.NewServer(emu)
	t.Cleanup(es.Close)
	c, err := config.Parse([]byte(`
groups:
  lanescape/browser:
    r2:
      account_id: 0123456789abcdef0123456789abcdef
      bucket: bucket-one
      access_key_id_secret_name: lanescape-downloads-id
      secret_access_key_secret_name: lanescape-downloads-secret
`))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "secrets.yaml")
	if err := os.WriteFile(path, []byte("lanescape-downloads-id: id\nlanescape-downloads-secret: "+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := secrets.File(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := app.New(c, app.Options{Secrets: src, R2Endpoint: es.URL, Log: slog.New(slog.NewJSONHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return c, s.URL, es.URL
}

// emulator is the provider's endpoint in these tests: where the service
// redirects to.
func emulator(base string) func(config.Group) string {
	return func(config.Group) string { return base }
}

func TestSmoke(t *testing.T) {
	c, base, emu := smokeWorld(t, "secret")
	var out bytes.Buffer
	if err := smoke(context.Background(), c, base, http.DefaultTransport, &out, emulator(emu)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "lanescape/browser: redirected, and R2 accepted the signature") {
		t.Errorf("out %q", out.String())
	}
	if strings.Contains(out.String(), "X-Amz") {
		t.Fatal("printed the signed URL")
	}
}

func TestSmokeFails(t *testing.T) {
	c, base, emu := smokeWorld(t, "wrong")
	err := smoke(context.Background(), c, base, http.DefaultTransport, io.Discard, emulator(emu))
	if err == nil || !strings.Contains(err.Error(), "SignatureDoesNotMatch") || strings.Contains(err.Error(), "X-Amz") {
		t.Fatalf("err = %v", err)
	}
	// A redirect to another host than the group's account's.
	c, base, _ = smokeWorld(t, "secret")
	err = smoke(context.Background(), c, base, http.DefaultTransport, io.Discard, r2Endpoint)
	if err == nil || !strings.Contains(err.Error(), "isn't to smoke-check/absent in bucket bucket-one") {
		t.Fatalf("err = %v", err)
	}
	// A redirect to another object, or one whose Location can't be parsed,
	// is refused without repeating it.
	for _, location := range []string{
		"http://HOST/other-bucket/smoke-check/absent?X-Amz-Signature=SIGNED",
		"http://HOST/bucket-one/other?X-Amz-Signature=SIGNED",
		"http://HOST/bucket-one/smoke-check/absent?X-Amz-Signature=SIGNED\x7f%zz",
		":%zz X-Amz-Signature=SIGNED",
	} {
		wrong := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header()["Location"] = []string{strings.ReplaceAll(location, "HOST", r.Host)}
			w.WriteHeader(http.StatusTemporaryRedirect)
		}))
		err := smoke(context.Background(), c, wrong.URL, http.DefaultTransport, io.Discard, emulator(wrong.URL))
		wrong.Close()
		if err == nil || strings.Contains(err.Error(), "SIGNED") {
			t.Errorf("%q: err = %v", location, err)
		}
	}
	// No redirect at all.
	plain := httptest.NewServer(http.NotFoundHandler())
	defer plain.Close()
	if err := smoke(context.Background(), c, plain.URL, http.DefaultTransport, io.Discard, emulator(plain.URL)); err == nil || !strings.Contains(err.Error(), "not a redirect") {
		t.Fatalf("err = %v", err)
	}
	if err := smoke(context.Background(), c, "ftp://x", http.DefaultTransport, io.Discard, emulator(plain.URL)); err == nil {
		t.Fatal("accepted ftp")
	}
}
