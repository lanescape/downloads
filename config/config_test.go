package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

const account = "0123456789abcdef0123456789abcdef"

func group(path, bucket, prefix string) string {
	return `
  ` + path + `:
    r2:
      account_id: ` + account + `
      bucket: ` + bucket + `
      access_key_id_secret_name: ` + prefix + `-access
      secret_access_key_secret_name: ` + prefix + `-secret
`
}

func TestEmbedded(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Groups) != 1 || c.Groups[0].Path != "lanescape/browser" || c.Groups[0].R2.Bucket != "lanescape-browser" {
		t.Fatalf("groups %+v", c.Groups)
	}
	// The names are exactly as written, not derived from anything.
	want := []string{
		"lanescape-downloads-lanescape-browser-r2-access-key-id",
		"lanescape-downloads-lanescape-browser-r2-secret-access-key",
	}
	if got := c.SecretNames(); !slices.Equal(got, want) {
		t.Errorf("SecretNames = %v, want %v", got, want)
	}
}

func TestParse(t *testing.T) {
	c, err := Parse([]byte("url_lifetime: 15m\ngroups:" +
		group("b/two", "bucket-two", "lanescape-downloads-x") +
		group("a", "bucket-one", "lanescape-downloads-anything-at-all")))
	if err != nil {
		t.Fatal(err)
	}
	if c.URLLifetime != 15*time.Minute {
		t.Errorf("URLLifetime = %s", c.URLLifetime)
	}
	if len(c.Groups) != 2 || c.Groups[0].Path != "a" || c.Groups[1].Path != "b/two" {
		t.Fatalf("groups %+v", c.Groups)
	}
	// A secret's name is taken verbatim, whatever it says.
	if got := c.Groups[0].R2.AccessKeyIDSecretName; got != "lanescape-downloads-anything-at-all-access" {
		t.Errorf("name %q", got)
	}
	want := []string{
		"lanescape-downloads-anything-at-all-access",
		"lanescape-downloads-anything-at-all-secret",
		"lanescape-downloads-x-access",
		"lanescape-downloads-x-secret",
	}
	if got := c.SecretNames(); !slices.Equal(got, want) {
		t.Errorf("SecretNames = %v", got)
	}
}

func TestDefaultLifetime(t *testing.T) {
	c, err := Parse([]byte("groups:" + group("a", "bucket-one", "lanescape-downloads-a")))
	if err != nil {
		t.Fatal(err)
	}
	if c.URLLifetime != DefaultURLLifetime {
		t.Errorf("URLLifetime = %s", c.URLLifetime)
	}
}

func TestRefused(t *testing.T) {
	for _, tc := range []struct{ name, yaml, want string }{
		{"empty", "", "empty"},
		{"no groups", "url_lifetime: 1h\n", "no groups"},
		{"unknown field", "groups:" + group("a", "bucket-one", "lanescape-downloads-a") + "other: 1\n", "other"},
		{"unknown provider field", strings.Replace("groups:"+group("a", "bucket-one", "lanescape-downloads-a"), "bucket:", "region: x\n      bucket:", 1), "region"},
		// A field holding a secret's value would be refused for its name.
		{"access_key_id field", strings.Replace("groups:"+group("a", "bucket-one", "lanescape-downloads-a"), "access_key_id_secret_name:", "access_key_id:", 1), "access_key_id"},
		{"short lifetime", "url_lifetime: 30s\ngroups:" + group("a", "bucket-one", "lanescape-downloads-a"), "between"},
		{"long lifetime", "url_lifetime: 169h\ngroups:" + group("a", "bucket-one", "lanescape-downloads-a"), "between"},
		{"bad lifetime", "url_lifetime: soon\ngroups:" + group("a", "bucket-one", "lanescape-downloads-a"), "url_lifetime"},
		{"uppercase group", "groups:" + group("Lanescape/b", "bucket-one", "lanescape-downloads-a"), "lowercase"},
		{"trailing slash", "groups:" + group("a/", "bucket-one", "lanescape-downloads-a"), "lowercase"},
		{"empty segment", "groups:" + group("a//b", "bucket-one", "lanescape-downloads-a"), "lowercase"},
		{"no provider", "groups:\n  a: {}\n", "no provider"},
		{"bad account", strings.Replace("groups:"+group("a", "bucket-one", "lanescape-downloads-a"), account, "ACCOUNT", 1), "account_id"},
		{"bad bucket", "groups:" + group("a", "Bucket_One", "lanescape-downloads-a"), "bucket"},
		{"unprefixed secret", "groups:" + group("a", "bucket-one", "other-team"), "lanescape-downloads-"},
		{"bad secret name", "groups:" + group("a", "bucket-one", "lanescape-downloads-a/b"), "Secret Manager"},
		{"no secret", strings.Replace("groups:"+group("a", "bucket-one", "lanescape-downloads-a"), "lanescape-downloads-a-secret", "''", 1), "no secret named"},
		{"secret used twice", "groups:" + group("a", "bucket-one", "lanescape-downloads-a") + group("b", "bucket-two", "lanescape-downloads-a"), "named by both"},
		{"secret used twice in one group", strings.Replace("groups:"+group("a", "bucket-one", "lanescape-downloads-a"), "lanescape-downloads-a-secret", "lanescape-downloads-a-access", 1), "named by both"},
		{"nested group", "groups:" + group("a", "bucket-one", "lanescape-downloads-a") + group("a/b", "bucket-two", "lanescape-downloads-b"), "within"},
		{"same slug", "groups:" + group("a-b/c", "bucket-one", "lanescape-downloads-a") + group("a/b-c", "bucket-two", "lanescape-downloads-b"), "same slug"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}
