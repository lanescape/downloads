package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lanescape/downloads/config"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func quiet() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

func TestSecretNames(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"-secret-names"}, env(nil), &out, quiet(), nil); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(out.String()), strings.Join(c.SecretNames(), ","); got != want || got == "" {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestArgs(t *testing.T) {
	if err := run(context.Background(), []string{"extra"}, env(nil), io.Discard, quiet(), nil); err == nil {
		t.Error("accepted an argument")
	}
	if err := run(context.Background(), []string{"-nope"}, env(nil), io.Discard, quiet(), nil); err == nil {
		t.Error("accepted an unknown flag")
	}
}

// Every secret the embedded configuration names, in a directory as Cloud
// Run mounts them.
func secretsDir(t *testing.T, skip string) string {
	t.Helper()
	c, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, name := range c.SecretNames() {
		if name == skip {
			continue
		}
		if err := os.MkdirAll(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name, "value"), []byte("value-"+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestServes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addrc := make(chan net.Addr, 1)
	errc := make(chan error, 1)
	go func() {
		errc <- run(ctx, nil, env(map[string]string{"PORT": "0", "DOWNLOADS_SECRETS_DIR": secretsDir(t, "")}), io.Discard, quiet(), func(a net.Addr) { addrc <- a })
	}()
	var addr net.Addr
	select {
	case addr = <-addrc:
	case err := <-errc:
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("didn't start")
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get("http://" + addr.String() + "/lanescape/browser/v1/latest")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusTemporaryRedirect || !strings.HasPrefix(loc, "https://") || !strings.Contains(loc, ".r2.cloudflarestorage.com/lanescape-browser/v1/latest?") {
		t.Fatalf("%d %q", resp.StatusCode, loc)
	}
	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("didn't stop")
	}
}

func TestRefusesWithoutSecrets(t *testing.T) {
	c, _ := config.Load()
	missing := c.SecretNames()[0]
	err := run(context.Background(), nil, env(map[string]string{"PORT": "0", "DOWNLOADS_SECRETS_DIR": secretsDir(t, missing)}), io.Discard, quiet(), nil)
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("err = %v", err)
	}
	err = run(context.Background(), nil, env(map[string]string{"PORT": "0", "DOWNLOADS_SECRETS_FILE": filepath.Join(t.TempDir(), "none")}), io.Discard, quiet(), nil)
	if err == nil {
		t.Fatal("started without its secrets file")
	}
}
