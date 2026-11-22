package r2

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// AWS's published examples of signed requests, from "Signature Calculations
// for the Authorization Header" and "Authenticating Requests: Using Query
// Parameters" in the Amazon S3 API reference.
const (
	exampleKeyID  = "AKIAIOSFODNN7EXAMPLE"
	exampleSecret = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	emptyHash     = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

var exampleTime = time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)

func exampleRequest(t *testing.T, method, target string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(method, "https://examplebucket.s3.amazonaws.com"+target, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Host = "examplebucket.s3.amazonaws.com"
	return r
}

func check(t *testing.T, r *http.Request, now time.Time) *authError {
	t.Helper()
	s, err := parse(r)
	if err != nil {
		return err
	}
	return s.verify(r, exampleSecret, now)
}

func TestAWSGetObjectExample(t *testing.T) {
	r := exampleRequest(t, "GET", "/test.txt")
	r.Header.Set("Range", "bytes=0-9")
	r.Header.Set("X-Amz-Content-Sha256", emptyHash)
	r.Header.Set("X-Amz-Date", "20130524T000000Z")
	r.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request,SignedHeaders=host;range;x-amz-content-sha256;x-amz-date,Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41")
	if err := check(t, r, exampleTime); err != nil {
		t.Fatal(err)
	}
	// A signed header changed.
	r.Header.Set("Range", "bytes=0-10")
	if err := check(t, r, exampleTime); err == nil || err.code != "SignatureDoesNotMatch" {
		t.Fatalf("changed range: %v", err)
	}
}

func TestAWSPutObjectExample(t *testing.T) {
	// The key, test$file.text, has a character the canonical URI escapes.
	r := exampleRequest(t, "PUT", "/test$file.text")
	r.Header.Set("Date", "Fri, 24 May 2013 00:00:00 GMT")
	r.Header.Set("X-Amz-Date", "20130524T000000Z")
	r.Header.Set("X-Amz-Storage-Class", "REDUCED_REDUNDANCY")
	r.Header.Set("X-Amz-Content-Sha256", "44ce7dd67c959e0d3524ffac1771dfbba87d2b6b4b4e99e42034a8b803f8b072")
	r.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request,SignedHeaders=date;host;x-amz-content-sha256;x-amz-date;x-amz-storage-class,Signature=98ad721746da40c64f1a55b78f14c238d841ea1380cd77a1b5971af0ece108bd")
	if err := check(t, r, exampleTime); err != nil {
		t.Fatal(err)
	}
}

const examplePresigned = "/test.txt?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=AKIAIOSFODNN7EXAMPLE%2F20130524%2Fus-east-1%2Fs3%2Faws4_request&X-Amz-Date=20130524T000000Z&X-Amz-Expires=86400&X-Amz-SignedHeaders=host&X-Amz-Signature=aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404"

func TestAWSPresignedExample(t *testing.T) {
	r := exampleRequest(t, "GET", examplePresigned)
	if err := check(t, r, exampleTime.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// The last second it's valid, then expired.
	if err := check(t, r, exampleTime.Add(86400*time.Second)); err != nil {
		t.Fatalf("at its expiry: %v", err)
	}
	if err := check(t, r, exampleTime.Add(86401*time.Second)); err == nil || err.status != 403 || err.code != "ExpiredRequest" {
		t.Fatalf("after its expiry: %v", err)
	}
	// Another method, or another object, isn't what was signed.
	if err := check(t, exampleRequest(t, "HEAD", examplePresigned), exampleTime); err == nil || err.code != "SignatureDoesNotMatch" {
		t.Fatalf("HEAD: %v", err)
	}
	if err := check(t, exampleRequest(t, "GET", strings.Replace(examplePresigned, "test.txt", "test2.txt", 1)), exampleTime); err == nil || err.code != "SignatureDoesNotMatch" {
		t.Fatalf("another key: %v", err)
	}
	// Nor a longer expiry.
	if err := check(t, exampleRequest(t, "GET", strings.Replace(examplePresigned, "Expires=86400", "Expires=86401", 1)), exampleTime); err == nil || err.code != "SignatureDoesNotMatch" {
		t.Fatalf("longer expiry: %v", err)
	}
}

func TestMalformed(t *testing.T) {
	for _, target := range []string{
		strings.Replace(examplePresigned, "Expires=86400", "Expires=604801", 1),
		strings.Replace(examplePresigned, "Expires=86400", "Expires=0", 1),
		strings.Replace(examplePresigned, "us-east-1", "eu-west-1", 1),
		strings.Replace(examplePresigned, "20130524%2F", "20130525%2F", 1),
		strings.Replace(examplePresigned, "X-Amz-SignedHeaders=host", "X-Amz-SignedHeaders=x-amz-date", 1),
		strings.Replace(examplePresigned, "AWS4-HMAC-SHA256", "AWS4-HMAC-SHA1", 1),
		"/test.txt",
	} {
		if err := check(t, exampleRequest(t, "GET", target), exampleTime); err == nil {
			t.Errorf("accepted %s", target)
		}
	}
	// A header-signed request outside the allowed skew.
	r := exampleRequest(t, "GET", "/test.txt")
	r.Header.Set("Range", "bytes=0-9")
	r.Header.Set("X-Amz-Content-Sha256", emptyHash)
	r.Header.Set("X-Amz-Date", "20130524T000000Z")
	r.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request,SignedHeaders=host;range;x-amz-content-sha256;x-amz-date,Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41")
	if err := check(t, r, exampleTime.Add(16*time.Minute)); err == nil || err.code != "RequestTimeTooSkewed" {
		t.Errorf("skewed: %v", err)
	}
}
