package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuild(t *testing.T) {
	file := filepath.Join(t.TempDir(), "latest")
	if err := os.WriteFile(file, []byte("20261122.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e, err := build([]string{"id:secret:read:a,b", "w:ws:readwrite:a"}, []string{"a/v1/latest=" + file})
	if err != nil {
		t.Fatal(err)
	}
	if o := e.Get("a", "v1/latest"); o == nil || string(o.Body) != "20261122.1\n" {
		t.Fatalf("object %+v", o)
	}
	for _, bad := range [][2][]string{
		{{"id:secret:write:a"}, nil},
		{{"id:secret:read"}, nil},
		{{":secret:read:a"}, nil},
		{nil, {"a/v1/latest"}},
		{nil, {"a=" + file}},
		{nil, {"a/k=" + file + ".none"}},
	} {
		if _, err := build(bad[0], bad[1]); err == nil {
			t.Errorf("build(%v, %v) succeeded", bad[0], bad[1])
		}
	}
	// A refused token is named by its position, never by its ID or secret.
	for _, bad := range []string{"the-id:the-secret:write:a", "the-id-and-the-secret", "the-id:the-secret"} {
		_, err = build([]string{"ok:ok:read:a", bad}, nil)
		if err == nil || !strings.Contains(err.Error(), "-token 2") || strings.Contains(err.Error(), "the-secret") || strings.Contains(err.Error(), "the-id") {
			t.Errorf("err = %v", err)
		}
	}
}
