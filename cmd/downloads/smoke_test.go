package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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
func smokeWorld(t *testing.T, secret string) (*config.Config, string) {
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
	return c, s.URL
}

func anyHost(*url.URL) bool { return true }

func TestSmoke(t *testing.T) {
	c, base := smokeWorld(t, "secret")
	var out bytes.Buffer
	if err := smoke(context.Background(), c, base, &http.Client{}, &out, anyHost); err != nil {
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
	c, base := smokeWorld(t, "wrong")
	err := smoke(context.Background(), c, base, &http.Client{}, io.Discard, anyHost)
	if err == nil || !strings.Contains(err.Error(), "SignatureDoesNotMatch") || strings.Contains(err.Error(), "X-Amz") {
		t.Fatalf("err = %v", err)
	}
	// A redirect elsewhere than R2.
	c, base = smokeWorld(t, "secret")
	err = smoke(context.Background(), c, base, &http.Client{}, io.Discard, isR2)
	if err == nil || !strings.Contains(err.Error(), "isn't to R2") {
		t.Fatalf("err = %v", err)
	}
	// No redirect at all.
	plain := httptest.NewServer(http.NotFoundHandler())
	defer plain.Close()
	if err := smoke(context.Background(), c, plain.URL, &http.Client{}, io.Discard, anyHost); err == nil || !strings.Contains(err.Error(), "not a redirect") {
		t.Fatalf("err = %v", err)
	}
	if err := smoke(context.Background(), c, "ftp://x", &http.Client{}, io.Discard, anyHost); err == nil {
		t.Fatal("accepted ftp")
	}
}

func TestIsR2(t *testing.T) {
	for s, want := range map[string]bool{
		"https://0123.r2.cloudflarestorage.com/b/k?X-Amz-Signature=x": true,
		"http://0123.r2.cloudflarestorage.com/b/k":                    false,
		"https://r2.cloudflarestorage.com.evil.example/b/k":           false,
		"https://example.com/b/k":                                     false,
	} {
		u, _ := url.Parse(s)
		if isR2(u) != want {
			t.Errorf("isR2(%s) = %v", s, !want)
		}
	}
}
