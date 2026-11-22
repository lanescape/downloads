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
	"regexp"
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
	const none = "no S3 error"
	if len(body) > maxErrorBody {
		return none
	}
	dec := xml.NewDecoder(strings.NewReader(body))
	depth, codes, inCode, started := 0, 0, false, false
	var code strings.Builder
	for {
		before := dec.InputOffset()
		tok, err := dec.Token()
		if err != nil {
			// Ending before the root closes, or not XML at all.
			return none
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch {
			case depth == 0:
				// Before the root, only an XML declaration and space.
				if started || t.Name.Local != "Error" || !prolog.MatchString(body[:before]) {
					return none
				}
				started = true
			case depth == 1 && t.Name.Local == "Code":
				codes++
				inCode = true
			case inCode:
				return none
			}
			depth++
		case xml.EndElement:
			depth--
			if depth == 1 {
				inCode = false
			}
			if depth == 0 {
				// After the root, only space: no processing instruction,
				// CDATA, comment or text.
				if strings.Trim(body[dec.InputOffset():], " \t\r\n") != "" || codes != 1 {
					return none
				}
				if c := code.String(); knownCodes[c] {
					return c
				}
				return "an unrecognized code"
			}
		case xml.CharData:
			if inCode {
				code.Write(t)
			}
		case xml.ProcInst, xml.Comment, xml.Directive:
			// Nothing may split a code's text.
			if inCode {
				return none
			}
		}
	}
}

// prolog is what may come before an S3 error document's root.
var prolog = regexp.MustCompile(`^[ \t\r\n]*(<\?xml[ \t][^?]*\?>)?[ \t\r\n]*$`)

// knownCodes are the S3 errors R2 gives that the smoke check may name.
var knownCodes = map[string]bool{
	"NoSuchKey": true, "NoSuchBucket": true, "AccessDenied": true, "SignatureDoesNotMatch": true,
	"InvalidAccessKeyId": true, "Unauthorized": true, "ExpiredRequest": true, "RequestTimeTooSkewed": true, "InvalidArgument": true,
	"AuthorizationQueryParametersError": true, "InternalError": true, "ServiceUnavailable": true,
}
