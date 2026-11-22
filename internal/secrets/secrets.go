// Package secrets reads the values of the secrets the configuration names
// (specs/0001-service.md, Secrets). On Cloud Run each secret is mounted from
// Secret Manager at <dir>/<name>/value (infra/run.tf). For a local run or a
// test, a file can map names to values instead.
//
// A value is never logged, and no error carries one: errors name the secret.
package secrets

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Source gives a secret's value by its name.
type Source interface {
	Get(name string) (string, error)
}

// ErrMissing is wrapped by Get's error when a secret has no value.
var ErrMissing = errors.New("no value")

// Dir reads each secret from <dir>/<name>/value, where Cloud Run mounts it.
func Dir(dir string) Source {
	return dirSource(dir)
}

type dirSource string

func (d dirSource) Get(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return "", fmt.Errorf("secret %q: not a secret's name", name)
	}
	b, err := os.ReadFile(filepath.Join(string(d), name, "value"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("secret %s: %w at %s", name, ErrMissing, filepath.Join(string(d), name))
	}
	if err != nil {
		// The error names the file, never its contents.
		return "", fmt.Errorf("secret %s: %w", name, err)
	}
	return value(name, b)
}

// File reads a YAML or JSON file that maps secrets' names to their values,
// for running the service outside Cloud Run:
//
//	lanescape-downloads-lanescape-browser-r2-access-key-id: <value>
//	lanescape-downloads-lanescape-browser-r2-secret-access-key: <value>
func File(path string) (Source, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("secrets file: %w", err)
	}
	m := map[string]string{}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&m); err != nil {
		// yaml's errors can quote the document, which holds the values.
		return nil, fmt.Errorf("secrets file %s: not a map of secrets' names to their values", path)
	}
	return mapSource(m), nil
}

type mapSource map[string]string

func (m mapSource) Get(name string) (string, error) {
	v, ok := m[name]
	if !ok {
		return "", fmt.Errorf("secret %s: %w in the secrets file", name, ErrMissing)
	}
	return value(name, []byte(v))
}

// value is a secret's value without surrounding whitespace, which a value
// added with echo would end with; no credential begins or ends with any.
func value(name string, b []byte) (string, error) {
	v := strings.TrimSpace(string(b))
	if v == "" {
		return "", fmt.Errorf("secret %s: %w: it's empty", name, ErrMissing)
	}
	return v, nil
}
