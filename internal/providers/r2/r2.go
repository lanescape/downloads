// Package r2 is the Cloudflare R2 provider (specs/0001-service.md,
// Providers): it presigns URLs of a private bucket's objects on R2's S3 API,
// so that a client downloads straight from R2 without credentials of its own.
//
// A presigned URL is a credential for its object until it expires. Never log
// one.
package r2

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// Region is R2's only region for SigV4.
const Region = "auto"

// unsignedPayload is the payload hash of a presigned URL, whose request has
// no body.
const unsignedPayload = "UNSIGNED-PAYLOAD"

// Endpoint is the S3 API's endpoint for a Cloudflare account.
func Endpoint(accountID string) string {
	return "https://" + accountID + ".r2.cloudflarestorage.com"
}

// Bucket presigns URLs of one bucket's objects.
type Bucket struct {
	endpoint *url.URL
	bucket   string
	creds    aws.Credentials
	lifetime time.Duration
	signer   *v4.Signer
}

// New is a bucket at endpoint, such as Endpoint(accountID), signed for with
// an R2 API token's access key ID and secret access key. Each URL it
// presigns lasts lifetime, at most 7 days.
func New(endpoint, bucket, accessKeyID, secretAccessKey string, lifetime time.Duration) (*Bucket, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.User != nil {
		return nil, fmt.Errorf("r2: endpoint %q must be a URL with only a scheme and a host", endpoint)
	}
	u.Path = ""
	if bucket == "" || strings.Contains(bucket, "/") {
		return nil, fmt.Errorf("r2: bucket %q", bucket)
	}
	if accessKeyID == "" || secretAccessKey == "" {
		return nil, errors.New("r2: no credentials")
	}
	if lifetime < time.Second || lifetime > 7*24*time.Hour {
		return nil, fmt.Errorf("r2: a URL's lifetime must be between a second and 7 days, not %s", lifetime)
	}
	return &Bucket{
		endpoint: u,
		bucket:   bucket,
		creds:    aws.Credentials{AccessKeyID: accessKeyID, SecretAccessKey: secretAccessKey},
		lifetime: lifetime,
		signer:   v4.NewSigner(),
	}, nil
}

// Presign is a URL for method, GET or HEAD, of the object key, path-style,
// valid from now for the bucket's lifetime. The key must already be safe in
// a URL's path: the server allows only letters, digits, '.', '_', '-' and
// '/' (internal/server).
func (b *Bucket) Presign(ctx context.Context, method, key string, now time.Time) (string, error) {
	if method != http.MethodGet && method != http.MethodHead {
		return "", fmt.Errorf("r2: can't presign %s", method)
	}
	u := *b.endpoint
	u.Path = "/" + b.bucket + "/" + key
	return presign(ctx, b.signer, b.creds, method, &u, Region, now, b.lifetime)
}

// presign signs a request's URL with SigV4's query parameters, as S3
// expects: the path isn't escaped again, and the payload isn't signed.
func presign(ctx context.Context, signer *v4.Signer, creds aws.Credentials, method string, u *url.URL, region string, now time.Time, lifetime time.Duration) (string, error) {
	q := u.Query()
	q.Set("X-Amz-Expires", strconv.Itoa(int(lifetime/time.Second)))
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("r2: %w", err)
	}
	signed, _, err := signer.PresignHTTP(ctx, creds, req, unsignedPayload, "s3", region, now.UTC(), func(o *v4.SignerOptions) {
		o.DisableURIPathEscaping = true
	})
	if err != nil {
		// The signer's errors don't include the URL; keep it that way.
		return "", errors.New("r2: couldn't presign the URL")
	}
	return signed, nil
}
