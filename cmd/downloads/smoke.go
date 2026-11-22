package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lanescape/downloads/config"
)

// smokeKey is an object no group ever has. The provider's answer for it,
// 404 NoSuchKey, shows that the service's signature, and its token's access
// to the bucket, were accepted: a wrong one is refused with 403 first.
const smokeKey = "smoke-check/absent"

// smoke checks the deployed service at base, as CD does after each deploy
// (specs/0002-deployment.md, CI and CD): for every group, a GET of an
// absent object is redirected, and the provider answers the redirect with
// NoSuchKey. It never prints the redirect's URL, which is signed.
func smoke(ctx context.Context, c *config.Config, base string, client *http.Client, out io.Writer, isR2 func(*url.URL) bool) error {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Host == "" {
		return fmt.Errorf("smoke check: %q isn't the service's URL", base)
	}
	noFollow := *client
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for _, g := range c.Groups {
		target := strings.TrimSuffix(base, "/") + "/" + g.Path + "/" + smokeKey
		status, location, _, err := get(ctx, &noFollow, target)
		if err != nil {
			return fmt.Errorf("smoke check: %s: %w", g.Path, err)
		}
		if status != http.StatusTemporaryRedirect || location == "" {
			return fmt.Errorf("smoke check: %s: the service answered %d, not a redirect", g.Path, status)
		}
		l, err := url.Parse(location)
		if err != nil || !isR2(l) {
			return fmt.Errorf("smoke check: %s: the redirect isn't to R2's S3 API", g.Path)
		}
		status, _, body, err := get(ctx, &noFollow, location)
		if err != nil {
			// The client's error can hold the URL; say only what failed.
			return fmt.Errorf("smoke check: %s: couldn't fetch the redirect's URL", g.Path)
		}
		if status != http.StatusNotFound || !strings.Contains(body, "<Code>NoSuchKey</Code>") {
			return fmt.Errorf("smoke check: %s: R2 answered %d, %s, not 404 NoSuchKey: is the token's secret right, and may it read the bucket?", g.Path, status, code(body))
		}
		fmt.Fprintf(out, "%s: redirected, and R2 accepted the signature\n", g.Path)
	}
	return nil
}

// isR2 is whether a URL is R2's S3 API's.
func isR2(u *url.URL) bool {
	return u.Scheme == "https" && strings.HasSuffix(u.Host, ".r2.cloudflarestorage.com")
}

func get(ctx context.Context, client *http.Client, target string) (int, string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, "", "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, resp.Header.Get("Location"), string(b), nil
}

// code is an S3 error's code, or "no error code".
func code(body string) string {
	_, rest, ok := strings.Cut(body, "<Code>")
	if !ok {
		return "no error code"
	}
	c, _, _ := strings.Cut(rest, "</Code>")
	if len(c) > 64 {
		c = c[:64]
	}
	return c
}
