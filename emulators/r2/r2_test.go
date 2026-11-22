package r2

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// The emulator's own tests sign with AWS's SDK, which its verifier was
// written apart from.

var t0 = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

type client struct {
	t      *testing.T
	base   string
	id     string
	secret string
	now    time.Time
}

func setup(t *testing.T) (*Emulator, string) {
	e := New()
	e.Now = func() time.Time { return t0 }
	e.AddBucket("one")
	e.AddBucket("two")
	e.AddToken("rw", "rw-secret", ReadWrite, "one")
	e.AddToken("ro", "ro-secret", Read, "one")
	s := httptest.NewServer(e)
	t.Cleanup(s.Close)
	return e, s.URL
}

func (c client) do(method, path string, body []byte, headers map[string]string) *http.Response {
	c.t.Helper()
	r, err := http.NewRequest(method, c.base+path, bytes.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	hash := sha256Hex(body)
	r.Header.Set("X-Amz-Content-Sha256", hash)
	err = v4.NewSigner().SignHTTP(context.Background(), aws.Credentials{AccessKeyID: c.id, SecretAccessKey: c.secret}, r, hash, "s3", "auto", c.now, func(o *v4.SignerOptions) {
		o.DisableURIPathEscaping = true
	})
	if err != nil {
		c.t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func (c client) presign(method, path string, expires int) string {
	c.t.Helper()
	u, _ := url.Parse(c.base + path)
	q := u.Query()
	q.Set("X-Amz-Expires", strconv.Itoa(expires))
	u.RawQuery = q.Encode()
	r, _ := http.NewRequest(method, u.String(), nil)
	signed, _, err := v4.NewSigner().PresignHTTP(context.Background(), aws.Credentials{AccessKeyID: c.id, SecretAccessKey: c.secret}, r, unsignedPayload, "s3", "auto", c.now, func(o *v4.SignerOptions) {
		o.DisableURIPathEscaping = true
	})
	if err != nil {
		c.t.Fatal(err)
	}
	return signed
}

func status(t *testing.T, resp *http.Response, want int) []byte {
	t.Helper()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("%s %s: %d, want %d: %s", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, want, b)
	}
	return b
}

func TestPutAndGet(t *testing.T) {
	e, base := setup(t)
	rw := client{t, base, "rw", "rw-secret", t0}
	resp := rw.do("PUT", "/one/v1/a.tar.gz", []byte("package"), map[string]string{
		"Content-Type":      "application/gzip",
		"Cache-Control":     "public, max-age=31536000, immutable",
		"X-Amz-Meta-Sha256": "abc",
		"If-None-Match":     "*",
	})
	status(t, resp, 200)
	etag := resp.Header.Get("ETag")
	if want := `"` + md5Hex([]byte("package")) + `"`; etag != want {
		t.Fatalf("ETag %q", etag)
	}
	o := e.Get("one", "v1/a.tar.gz")
	if o == nil || string(o.Body) != "package" || o.ETag != etag || o.Meta["sha256"] != "abc" {
		t.Fatalf("stored %+v", o)
	}
	ro := client{t, base, "ro", "ro-secret", t0}
	resp = ro.do("GET", "/one/v1/a.tar.gz", nil, nil)
	if b := status(t, resp, 200); string(b) != "package" {
		t.Fatalf("body %q", b)
	}
	h := resp.Header
	if h.Get("ETag") != etag || h.Get("Content-Type") != "application/gzip" || h.Get("Cache-Control") != "public, max-age=31536000, immutable" || h.Get("X-Amz-Meta-Sha256") != "abc" {
		t.Errorf("headers %v", h)
	}
	resp = ro.do("HEAD", "/one/v1/a.tar.gz", nil, nil)
	if b := status(t, resp, 200); len(b) != 0 || resp.Header.Get("Content-Length") != "7" {
		t.Errorf("HEAD: %q %v", b, resp.Header)
	}
}

func TestConditionalWrites(t *testing.T) {
	_, base := setup(t)
	rw := client{t, base, "rw", "rw-secret", t0}
	first := rw.do("PUT", "/one/latest", []byte("1\n"), map[string]string{"If-None-Match": "*"})
	status(t, first, 200)
	etag := first.Header.Get("ETag")
	// Create-only fails once it exists.
	status(t, rw.do("PUT", "/one/latest", []byte("2\n"), map[string]string{"If-None-Match": "*"}), 412)
	// A swap from the ETag read succeeds once, then fails.
	second := rw.do("PUT", "/one/latest", []byte("2\n"), map[string]string{"If-Match": etag})
	status(t, second, 200)
	status(t, rw.do("PUT", "/one/latest", []byte("3\n"), map[string]string{"If-Match": etag}), 412)
	// If-Match on an absent object fails; * matches any that exists.
	status(t, rw.do("PUT", "/one/absent", []byte("x"), map[string]string{"If-Match": "*"}), 412)
	status(t, rw.do("PUT", "/one/latest", []byte("4\n"), map[string]string{"If-Match": "*"}), 200)
	// A weak ETag never matches.
	status(t, rw.do("PUT", "/one/latest", []byte("5\n"), map[string]string{"If-Match": "W/" + second.Header.Get("ETag")}), 412)
	// Unconditional writes overwrite.
	status(t, rw.do("PUT", "/one/latest", []byte("6\n"), nil), 200)
}

func TestRefusals(t *testing.T) {
	_, base := setup(t)
	rw := client{t, base, "rw", "rw-secret", t0}
	ro := client{t, base, "ro", "ro-secret", t0}
	status(t, ro.do("PUT", "/one/x", []byte("x"), nil), 403)
	status(t, rw.do("GET", "/two/x", nil, nil), 403)
	status(t, rw.do("GET", "/one/none", nil, nil), 404)
	wrong := client{t, base, "ro", "not-the-secret", t0}
	status(t, wrong.do("GET", "/one/none", nil, nil), 403)
	unknown := client{t, base, "nobody", "x", t0}
	status(t, unknown.do("GET", "/one/none", nil, nil), 403)
	resp, err := http.Get(base + "/one/x")
	if err != nil {
		t.Fatal(err)
	}
	status(t, resp, 403)
	// A body that isn't what was signed.
	r, _ := http.NewRequest("PUT", base+"/one/x", bytes.NewReader([]byte("other")))
	hash := sha256Hex([]byte("signed"))
	r.Header.Set("X-Amz-Content-Sha256", hash)
	_ = v4.NewSigner().SignHTTP(context.Background(), aws.Credentials{AccessKeyID: "rw", SecretAccessKey: "rw-secret"}, r, hash, "s3", "auto", t0)
	resp, err = http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	status(t, resp, 400)
	// Not emulated.
	status(t, rw.do("DELETE", "/one/x", nil, nil), 501)
	status(t, rw.do("GET", "/one", nil, nil), 501)
}

func TestPresigned(t *testing.T) {
	e, base := setup(t)
	e.Put("one", "v1/latest", []byte("20261122.1\n"), "text/plain")
	ro := client{t, base, "ro", "ro-secret", t0}
	u := ro.presign("GET", "/one/v1/latest", 3600)
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	if b := status(t, resp, 200); string(b) != "20261122.1\n" {
		t.Fatalf("body %q", b)
	}
	// A GET's URL isn't a HEAD's.
	r, _ := http.NewRequest("HEAD", u, nil)
	resp, err = http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	status(t, resp, 403)
	h := ro.presign("HEAD", "/one/v1/latest", 3600)
	r, _ = http.NewRequest("HEAD", h, nil)
	resp, err = http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	status(t, resp, 200)
	// Expired.
	e.Now = func() time.Time { return t0.Add(3601 * time.Second) }
	resp, err = http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	status(t, resp, 403)
}

func md5Hex(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}
