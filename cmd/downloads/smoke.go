package main

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/lanescape/downloads/config"
	"github.com/lanescape/downloads/internal/providers/r2"
)

// smokeKey is an object no group ever has. The provider's answer for it,
// 404 NoSuchKey, shows that the service's signature, and its token's access
// to the bucket, were accepted: a wrong one is refused with 403 first.
const smokeKey = "smoke-check/absent"

// smoke checks the deployed service at base, as CD does after each deploy
// (specs/0002-deployment.md, CI and CD): for every group, a GET of an
// absent object is redirected to exactly that object in the group's bucket,
// and the provider answers the redirect with NoSuchKey. endpoint gives a
// group's provider's scheme and host. Each request is made by itself,
// without following redirects, so nothing parses or reports a redirect's
// URL, which is signed.
func smoke(ctx context.Context, c *config.Config, base string, transport http.RoundTripper, out io.Writer, endpoint func(config.Group) string) error {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Host == "" {
		return fmt.Errorf("smoke check: %q isn't the service's URL", base)
	}
	for _, g := range c.Groups {
		target := strings.TrimSuffix(base, "/") + "/" + g.Path + "/" + smokeKey
		status, location, _, err := get(ctx, transport, target)
		if err != nil {
			// Not the error itself: an answer that can't be read can quote
			// its Location, which is signed.
			return fmt.Errorf("smoke check: %s: couldn't get an answer from the service at %s", g.Path, base)
		}
		if status != http.StatusTemporaryRedirect || location == "" {
			return fmt.Errorf("smoke check: %s: the service answered %d, not a redirect", g.Path, status)
		}
		want, err := url.Parse(endpoint(g))
		if err != nil {
			return fmt.Errorf("smoke check: %s: the provider's endpoint", g.Path)
		}
		l, err := url.Parse(location)
		// The path as sent, so an escaped separator doesn't pass for one.
		if err != nil || l.Scheme != want.Scheme || l.Host != want.Host || l.EscapedPath() != "/"+g.R2.Bucket+"/"+smokeKey {
			return fmt.Errorf("smoke check: %s: the redirect isn't to %s in bucket %s", g.Path, smokeKey, g.R2.Bucket)
		}
		status, _, body, err := get(ctx, transport, location)
		if err != nil {
			// The error would hold the signed URL; say only what failed.
			return fmt.Errorf("smoke check: %s: couldn't fetch the redirect's URL", g.Path)
		}
		if c := s3Code(body); status != http.StatusNotFound || c != "NoSuchKey" {
			return fmt.Errorf("smoke check: %s: R2 answered %d, %s, not 404 NoSuchKey: is the token's secret right, and may it read the bucket?", g.Path, status, c)
		}
		fmt.Fprintf(out, "%s: redirected, and R2 accepted the signature\n", g.Path)
	}
	return nil
}

// r2Endpoint is a group's R2 account's S3 API.
func r2Endpoint(g config.Group) string {
	return r2.Endpoint(g.R2.AccountID)
}

// get makes one request, following nothing.
func get(ctx context.Context, transport http.RoundTripper, target string) (int, string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, "", "", err
	}
	resp, err := transport.RoundTrip(req)
	if err != nil {
		return 0, "", "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return 0, "", "", err
	}
	return resp.StatusCode, resp.Header.Get("Location"), string(b), nil
}

// s3Code is an S3 error document's code, or "no S3 error" if the body isn't
// one, or its code isn't a plain word, which is never repeated.
func s3Code(body string) string {
	var e struct {
		XMLName xml.Name `xml:"Error"`
		Code    string   `xml:"Code"`
	}
	if xml.Unmarshal([]byte(body), &e) != nil || !s3CodeWord.MatchString(e.Code) {
		return "no S3 error"
	}
	return e.Code
}

var s3CodeWord = regexp.MustCompile(`^[A-Za-z]{1,64}$`)
