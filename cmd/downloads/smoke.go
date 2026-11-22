package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lanescape/downloads/config"
	"github.com/lanescape/downloads/internal/providers/r2"
)

// smokeKey is an object no group has: a fresh random name for each check,
// so a publisher can't have one at it. The provider's answer for it, 404
// NoSuchKey, shows that the service's signature, and its token's access to
// the bucket, were accepted: a wrong one is refused with 403 first.
func smokeKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "smoke-check/" + hex.EncodeToString(b)
}

// maxErrorBody is the most of a provider's answer read; a longer one isn't
// an S3 error document.
const maxErrorBody = 64 << 10

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
	key := smokeKey()
	for _, g := range c.Groups {
		target := strings.TrimSuffix(base, "/") + "/" + g.Path + "/" + key
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
		if err != nil || l.Scheme != want.Scheme || l.Host != want.Host || l.EscapedPath() != "/"+g.R2.Bucket+"/"+key {
			return fmt.Errorf("smoke check: %s: the redirect isn't to %s in bucket %s", g.Path, key, g.R2.Bucket)
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
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody+1))
	if err != nil {
		return 0, "", "", err
	}
	return resp.StatusCode, resp.Header.Get("Location"), string(b), nil
}

// s3Code is an S3 error document's code: the body must be exactly one
// <Error> element, with exactly one <Code>, and nothing after it but space.
// It's "no S3 error" for anything else, and "an unrecognized code" for a
// code that isn't one R2 is known to give, so nothing the provider reflects
// is ever repeated.
func s3Code(body string) string {
	if len(body) > maxErrorBody {
		return "no S3 error"
	}
	dec := xml.NewDecoder(strings.NewReader(body))
	depth, roots, codes, inCode := 0, 0, 0, false
	var code strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "no S3 error"
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots > 1 || t.Name.Local != "Error" {
					return "no S3 error"
				}
			} else if depth == 1 && t.Name.Local == "Code" {
				codes++
				inCode = true
			} else if inCode {
				return "no S3 error"
			}
			depth++
		case xml.EndElement:
			depth--
			if depth == 1 {
				inCode = false
			}
		case xml.CharData:
			if inCode {
				code.Write(t)
			} else if depth == 0 && strings.TrimSpace(string(t)) != "" {
				return "no S3 error"
			}
		case xml.Comment, xml.Directive:
			if depth == 0 || inCode {
				return "no S3 error"
			}
		}
	}
	if roots != 1 || codes != 1 {
		return "no S3 error"
	}
	if c := code.String(); knownCodes[c] {
		return c
	}
	return "an unrecognized code"
}

// knownCodes are the S3 errors R2 gives that the smoke check may name.
var knownCodes = map[string]bool{
	"NoSuchKey": true, "NoSuchBucket": true, "AccessDenied": true, "SignatureDoesNotMatch": true,
	"InvalidAccessKeyId": true, "ExpiredRequest": true, "RequestTimeTooSkewed": true, "InvalidArgument": true,
	"AuthorizationQueryParametersError": true, "InternalError": true, "ServiceUnavailable": true,
}
