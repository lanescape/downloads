package r2

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// AWS's published example of a presigned URL, from "Authenticating
// Requests: Using Query Parameters (AWS Signature Version 4)" in the Amazon
// S3 API reference: a GET of examplebucket's test.txt, for a day from
// 2013-05-24 00:00:00 UTC.
func TestPresignAWSExample(t *testing.T) {
	u, _ := url.Parse("https://examplebucket.s3.amazonaws.com/test.txt")
	creds := aws.Credentials{AccessKeyID: "AKIAIOSFODNN7EXAMPLE", SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}
	now := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	got, err := presign(context.Background(), v4.NewSigner(), creds, "GET", u, "us-east-1", now, 86400*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	q := signed.Query()
	want := map[string]string{
		"X-Amz-Algorithm":     "AWS4-HMAC-SHA256",
		"X-Amz-Credential":    "AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request",
		"X-Amz-Date":          "20130524T000000Z",
		"X-Amz-Expires":       "86400",
		"X-Amz-SignedHeaders": "host",
		"X-Amz-Signature":     "aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404",
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	if signed.Host != "examplebucket.s3.amazonaws.com" || signed.Path != "/test.txt" {
		t.Errorf("signed %s", signed.Redacted())
	}
}

func TestPresign(t *testing.T) {
	b, err := New(Endpoint("0123456789abcdef0123456789abcdef"), "lanescape-browser", "AKID", "SECRET", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	for _, method := range []string{"GET", "HEAD"} {
		got, err := b.Presign(context.Background(), method, "v1/20261122.1/manifest.json", now)
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(got)
		if err != nil {
			t.Fatal(err)
		}
		if u.Scheme != "https" || u.Host != "0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com" || u.Path != "/lanescape-browser/v1/20261122.1/manifest.json" {
			t.Errorf("%s: %s", method, u.Redacted())
		}
		q := u.Query()
		if q.Get("X-Amz-Credential") != "AKID/20261122/auto/s3/aws4_request" || q.Get("X-Amz-Expires") != "3600" || q.Get("X-Amz-Signature") == "" {
			t.Errorf("%s: query %v", method, q)
		}
		if strings.Contains(got, "SECRET") {
			t.Fatal("the URL holds the secret")
		}
	}
	get, _ := b.Presign(context.Background(), "GET", "a", now)
	head, _ := b.Presign(context.Background(), "HEAD", "a", now)
	if get == head {
		t.Error("GET's and HEAD's URLs are the same: the method isn't signed")
	}
	if _, err := b.Presign(context.Background(), "PUT", "a", now); err == nil {
		t.Error("presigned a PUT")
	}
}

func TestNewRefuses(t *testing.T) {
	for _, tc := range []struct {
		endpoint, bucket, id, secret string
		lifetime                     time.Duration
	}{
		{"ftp://x", "b", "i", "s", time.Hour},
		{"https://", "b", "i", "s", time.Hour},
		{"https://x/path", "b", "i", "s", time.Hour},
		{"https://x?q=1", "b", "i", "s", time.Hour},
		{"https://u:p@x", "b", "i", "s", time.Hour},
		{"https://x", "", "i", "s", time.Hour},
		{"https://x", "a/b", "i", "s", time.Hour},
		{"https://x", "b", "", "s", time.Hour},
		{"https://x", "b", "i", "", time.Hour},
		{"https://x", "b", "i", "s", 0},
		{"https://x", "b", "i", "s", 8 * 24 * time.Hour},
	} {
		if _, err := New(tc.endpoint, tc.bucket, tc.id, tc.secret, tc.lifetime); err == nil {
			t.Errorf("New(%q, %q, %q, %q, %s) succeeded", tc.endpoint, tc.bucket, tc.id, tc.secret, tc.lifetime)
		}
	}
	if _, err := New("http://127.0.0.1:9000/", "b", "i", "s", time.Hour); err != nil {
		t.Errorf("an emulator's endpoint: %v", err)
	}
}
