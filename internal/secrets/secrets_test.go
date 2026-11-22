package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const value1 = "s3cr3t-value-that-must-not-leak"

func TestDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a", "value"), []byte(value1+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty", "value"), []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := Dir(dir)
	got, err := s.Get("a")
	if err != nil || got != value1 {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if _, err := s.Get("missing"); !errors.Is(err, ErrMissing) || !strings.Contains(err.Error(), "missing") {
		t.Errorf("missing: %v", err)
	}
	if _, err := s.Get("empty"); !errors.Is(err, ErrMissing) {
		t.Errorf("empty: %v", err)
	}
	for _, name := range []string{"", ".", "..", "a/b", `a\b`} {
		if _, err := s.Get(name); err == nil {
			t.Errorf("Get(%q) succeeded", name)
		}
	}
}

func TestFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.yaml")
	if err := os.WriteFile(path, []byte("a: "+value1+"\nb: ''\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := File(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get("a"); err != nil || got != value1 {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if _, err := s.Get("b"); !errors.Is(err, ErrMissing) {
		t.Errorf("b: %v", err)
	}
	if _, err := s.Get("c"); !errors.Is(err, ErrMissing) {
		t.Errorf("c: %v", err)
	}
}

func TestFileJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	if err := os.WriteFile(path, []byte(`{"a": "`+value1+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := File(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get("a"); got != value1 {
		t.Fatalf("Get = %q", got)
	}
}

// A file that isn't a map is refused without quoting it.
func TestFileErrorsHoldNoValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.yaml")
	if err := os.WriteFile(path, []byte("- "+value1+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := File(path)
	if err == nil {
		t.Fatal("accepted a list")
	}
	if strings.Contains(err.Error(), value1) {
		t.Fatalf("the error quotes the value: %v", err)
	}
	if _, err := File(filepath.Join(t.TempDir(), "none")); err == nil {
		t.Fatal("accepted a missing file")
	}
}
