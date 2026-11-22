// Package config is the service's configuration: the groups it serves and
// each group's provider (specs/0001-service.md, Configuration). groups.yaml,
// beside this file, is embedded into the service's binary and validated when
// it starts.
//
// The configuration holds only what isn't secret. A provider's credentials
// are named, never given: each field ending in _secret_name holds the name of
// a Secret Manager secret, written out in full, and the service reads that
// secret's value at startup (internal/secrets). Nothing derives a secret's
// name from a group's or a provider's.
package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

//go:embed groups.yaml
var groups []byte

// SecretPrefix starts every secret's name. The project is shared with other
// teams, and the infra identity's grants on secrets are limited to names that
// start with it (bootstrap/roles.tf).
const SecretPrefix = "lanescape-downloads-"

// MaxURLLifetime is the longest a presigned URL may last: R2's limit, as
// S3's.
const MaxURLLifetime = 7 * 24 * time.Hour

// MinURLLifetime is the shortest: a client must be able to follow the
// redirect.
const MinURLLifetime = time.Minute

// DefaultURLLifetime is a presigned URL's lifetime when the configuration
// doesn't give one.
const DefaultURLLifetime = time.Hour

// Config is the whole configuration.
type Config struct {
	// URLLifetime is how long a presigned URL lasts.
	URLLifetime time.Duration
	// Groups are sorted by path.
	Groups []Group
}

// Group is a group of downloads: every path under Path is served by its
// provider.
type Group struct {
	// Path is the group's path in URLs, such as lanescape/browser.
	Path string
	// R2 is the group's provider, the only kind there is so far.
	R2 *R2
}

// R2 is a Cloudflare R2 bucket.
type R2 struct {
	// AccountID is the Cloudflare account's ID, which names the S3 API's
	// endpoint, https://<account ID>.r2.cloudflarestorage.com.
	AccountID string
	// Bucket is the bucket's name.
	Bucket string
	// AccessKeyIDSecretName is the name of the Secret Manager secret that
	// holds the access key ID of an R2 API token that may read the bucket.
	AccessKeyIDSecretName string
	// SecretAccessKeySecretName is the name of the Secret Manager secret
	// that holds that token's secret access key.
	SecretAccessKeySecretName string
}

type file struct {
	URLLifetime *string               `yaml:"url_lifetime"`
	Groups      map[string]*groupFile `yaml:"groups"`
}

type groupFile struct {
	R2 *r2File `yaml:"r2"`
}

type r2File struct {
	AccountID                 string `yaml:"account_id"`
	Bucket                    string `yaml:"bucket"`
	AccessKeyIDSecretName     string `yaml:"access_key_id_secret_name"`
	SecretAccessKeySecretName string `yaml:"secret_access_key_secret_name"`
}

var (
	segment    = `[a-z0-9]+(-[a-z0-9]+)*`
	groupPath  = regexp.MustCompile(`^` + segment + `(/` + segment + `)*$`)
	accountID  = regexp.MustCompile(`^[0-9a-f]{32}$`)
	bucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
	// A Secret Manager secret's ID.
	secretName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,255}$`)
)

// Load parses and validates the embedded groups.yaml.
func Load() (*Config, error) {
	return Parse(groups)
}

// Parse parses and validates a configuration. Unknown fields are refused.
func Parse(data []byte) (*Config, error) {
	var f file
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("config: empty")
		}
		return nil, fmt.Errorf("config: %w", err)
	}
	c := &Config{URLLifetime: DefaultURLLifetime}
	if f.URLLifetime != nil {
		d, err := time.ParseDuration(*f.URLLifetime)
		if err != nil {
			return nil, fmt.Errorf("config: url_lifetime: %w", err)
		}
		if d < MinURLLifetime || d > MaxURLLifetime {
			return nil, fmt.Errorf("config: url_lifetime %s must be between %s and %s", d, MinURLLifetime, MaxURLLifetime)
		}
		c.URLLifetime = d
	}
	if len(f.Groups) == 0 {
		return nil, errors.New("config: no groups")
	}
	slugs := map[string]string{}
	for path, g := range f.Groups {
		if !groupPath.MatchString(path) {
			return nil, fmt.Errorf("config: group %q: a group's path is lowercase letters, digits and hyphens, in segments separated by /", path)
		}
		slug := strings.ReplaceAll(path, "/", "-")
		if other, ok := slugs[slug]; ok {
			return nil, fmt.Errorf("config: groups %q and %q have the same slug, %s", other, path, slug)
		}
		slugs[slug] = path
		if g == nil || g.R2 == nil {
			return nil, fmt.Errorf("config: group %q has no provider", path)
		}
		r, err := parseR2(path, g.R2)
		if err != nil {
			return nil, err
		}
		c.Groups = append(c.Groups, Group{Path: path, R2: r})
	}
	slices.SortFunc(c.Groups, func(a, b Group) int { return strings.Compare(a.Path, b.Path) })
	// No group's path is within another's, so a request's group is never
	// ambiguous.
	for i, a := range c.Groups {
		for _, b := range c.Groups[i+1:] {
			if strings.HasPrefix(b.Path, a.Path+"/") {
				return nil, fmt.Errorf("config: group %q is within group %q", b.Path, a.Path)
			}
		}
	}
	// Each secret serves one field of one group.
	used := map[string]string{}
	for _, g := range c.Groups {
		for field, name := range g.R2.secretNames() {
			where := g.Path + " r2 " + field
			if other, ok := used[name]; ok {
				return nil, fmt.Errorf("config: secret %s is named by both %s and %s", name, other, where)
			}
			used[name] = where
		}
	}
	return c, nil
}

func parseR2(path string, f *r2File) (*R2, error) {
	if !accountID.MatchString(f.AccountID) {
		return nil, fmt.Errorf("config: group %q: r2 account_id must be a Cloudflare account ID, 32 lowercase hexadecimal digits", path)
	}
	if !bucketName.MatchString(f.Bucket) {
		return nil, fmt.Errorf("config: group %q: r2 bucket %q isn't an R2 bucket name", path, f.Bucket)
	}
	r := &R2{
		AccountID:                 f.AccountID,
		Bucket:                    f.Bucket,
		AccessKeyIDSecretName:     f.AccessKeyIDSecretName,
		SecretAccessKeySecretName: f.SecretAccessKeySecretName,
	}
	for field, name := range r.secretNames() {
		if err := checkSecretName(name); err != nil {
			return nil, fmt.Errorf("config: group %q: r2 %s: %w", path, field, err)
		}
	}
	return r, nil
}

func (r *R2) secretNames() map[string]string {
	return map[string]string{
		"access_key_id_secret_name":     r.AccessKeyIDSecretName,
		"secret_access_key_secret_name": r.SecretAccessKeySecretName,
	}
}

func checkSecretName(name string) error {
	if name == "" {
		return errors.New("no secret named")
	}
	if !secretName.MatchString(name) {
		return fmt.Errorf("%q isn't a Secret Manager secret's name", name)
	}
	if !strings.HasPrefix(name, SecretPrefix) {
		return fmt.Errorf("secret %s must be named %s...", name, SecretPrefix)
	}
	return nil
}

// SecretNames is the name of every secret the configuration names, sorted.
func (c *Config) SecretNames() []string {
	var names []string
	for _, g := range c.Groups {
		for _, name := range g.R2.secretNames() {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}
