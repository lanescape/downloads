package r2

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// This file verifies AWS Signature Version 4, as S3 and R2 use it, written
// from AWS's description of the algorithm rather than with the service's
// signer, so that the two check each other. sigv4_test.go holds it to AWS's
// published examples.

const (
	algorithm       = "AWS4-HMAC-SHA256"
	amzDateFormat   = "20060102T150405Z"
	unsignedPayload = "UNSIGNED-PAYLOAD"
	// maxPresignedExpiry is the longest a presigned URL may last.
	maxPresignedExpiry = 7 * 24 * 60 * 60
	// maxSkew is how far a signed request's date may be from the server's
	// clock.
	maxSkew = 15 * time.Minute
)

// signed is what a request's signature claims.
type signed struct {
	accessKeyID   string
	date          time.Time
	scope         string // <yyyymmdd>/<region>/s3/aws4_request
	signedHeaders []string
	signature     string
	payloadHash   string
	presigned     bool
	expires       int
}

// authError is a refusal, as S3's XML error.
type authError struct {
	status  int
	code    string
	message string
}

func (e *authError) Error() string { return e.code + ": " + e.message }

func refuse(status int, code, format string, args ...any) *authError {
	return &authError{status, code, fmt.Sprintf(format, args...)}
}

// parse reads a request's signature, from its Authorization header or its
// query.
func parse(r *http.Request) (*signed, *authError) {
	q := r.URL.Query()
	if auth := r.Header.Get("Authorization"); auth != "" {
		if q.Get("X-Amz-Signature") != "" {
			return nil, refuse(http.StatusBadRequest, "InvalidArgument", "only one auth mechanism allowed")
		}
		return parseHeader(r, auth)
	}
	if q.Get("X-Amz-Signature") != "" || q.Get("X-Amz-Algorithm") != "" {
		return parseQuery(q)
	}
	return nil, refuse(http.StatusForbidden, "AccessDenied", "no credentials")
}

func parseHeader(r *http.Request, auth string) (*signed, *authError) {
	rest, ok := strings.CutPrefix(auth, algorithm+" ")
	if !ok {
		return nil, refuse(http.StatusBadRequest, "InvalidArgument", "unsupported authorization type")
	}
	fields := map[string]string{}
	for part := range strings.SplitSeq(rest, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return nil, refuse(http.StatusBadRequest, "InvalidArgument", "malformed authorization header")
		}
		fields[k] = v
	}
	s, err := credential(fields["Credential"], r.Header.Get("X-Amz-Date"))
	if err != nil {
		return nil, err
	}
	s.signedHeaders = strings.Split(fields["SignedHeaders"], ";")
	s.signature = fields["Signature"]
	s.payloadHash = r.Header.Get("X-Amz-Content-Sha256")
	if s.payloadHash == "" {
		return nil, refuse(http.StatusBadRequest, "InvalidArgument", "x-amz-content-sha256 is required")
	}
	return s, nil
}

func parseQuery(q url.Values) (*signed, *authError) {
	if q.Get("X-Amz-Algorithm") != algorithm {
		return nil, refuse(http.StatusBadRequest, "InvalidArgument", "unsupported X-Amz-Algorithm")
	}
	s, err := credential(q.Get("X-Amz-Credential"), q.Get("X-Amz-Date"))
	if err != nil {
		return nil, err
	}
	s.presigned = true
	s.signedHeaders = strings.Split(q.Get("X-Amz-SignedHeaders"), ";")
	s.signature = q.Get("X-Amz-Signature")
	s.payloadHash = unsignedPayload
	expires, convErr := strconv.Atoi(q.Get("X-Amz-Expires"))
	if convErr != nil || expires < 1 || expires > maxPresignedExpiry {
		return nil, refuse(http.StatusBadRequest, "AuthorizationQueryParametersError", "X-Amz-Expires must be between 1 and %d seconds", maxPresignedExpiry)
	}
	s.expires = expires
	return s, nil
}

// credential reads <access key ID>/<date>/<region>/s3/aws4_request and the
// request's date, which must be the scope's day.
func credential(cred, amzDate string) (*signed, *authError) {
	parts := strings.Split(cred, "/")
	if len(parts) != 5 || parts[0] == "" || parts[3] != "s3" || parts[4] != "aws4_request" {
		return nil, refuse(http.StatusBadRequest, "AuthorizationHeaderMalformed", "malformed credential")
	}
	// R2's region is auto; S3 tools that can't say so may send us-east-1
	// or nothing, which R2 takes as auto.
	switch parts[2] {
	case "auto", "us-east-1", "":
	default:
		return nil, refuse(http.StatusBadRequest, "AuthorizationHeaderMalformed", "region %q is wrong; expecting 'auto'", parts[2])
	}
	date, err := time.Parse(amzDateFormat, amzDate)
	if err != nil {
		return nil, refuse(http.StatusBadRequest, "AuthorizationHeaderMalformed", "malformed date")
	}
	if date.Format("20060102") != parts[1] {
		return nil, refuse(http.StatusBadRequest, "AuthorizationHeaderMalformed", "the credential's date isn't the request's")
	}
	return &signed{accessKeyID: parts[0], date: date, scope: strings.Join(parts[1:], "/")}, nil
}

// verify checks the signature with the secret, and that it's in date at
// now.
func (s *signed) verify(r *http.Request, secret string, now time.Time) *authError {
	if !contains(s.signedHeaders, "host") {
		return refuse(http.StatusBadRequest, "AuthorizationHeaderMalformed", "host must be signed")
	}
	// A header-signed request signs every x-amz- header it sends, such as
	// the metadata a write stores, but x-amz-content-sha256, which S3
	// checks against the body anyway.
	if !s.presigned {
		for name := range r.Header {
			lower := strings.ToLower(name)
			if strings.HasPrefix(lower, "x-amz-") && lower != "x-amz-content-sha256" && !contains(s.signedHeaders, lower) {
				return refuse(http.StatusForbidden, "AccessDenied", "There were headers present in the request which were not signed")
			}
		}
	}
	want := signature(secret, s.date, s.scope, canonicalRequest(r, s))
	if !hmac.Equal([]byte(want), []byte(s.signature)) {
		return refuse(http.StatusForbidden, "SignatureDoesNotMatch", "the request signature we calculated does not match the signature you provided")
	}
	if s.presigned {
		if now.After(s.date.Add(time.Duration(s.expires) * time.Second)) {
			return refuse(http.StatusForbidden, "AccessDenied", "Request has expired")
		}
		if s.date.After(now.Add(maxSkew)) {
			return refuse(http.StatusForbidden, "AccessDenied", "Request is not yet valid")
		}
		return nil
	}
	if d := now.Sub(s.date); d > maxSkew || d < -maxSkew {
		return refuse(http.StatusForbidden, "RequestTimeTooSkewed", "the difference between the request time and the server's time is too large")
	}
	return nil
}

func canonicalRequest(r *http.Request, s *signed) string {
	var b strings.Builder
	b.WriteString(r.Method + "\n")
	b.WriteString(uriEncode(r.URL.Path, false) + "\n")
	b.WriteString(canonicalQuery(r.URL.Query(), s.presigned) + "\n")
	headers := append([]string(nil), s.signedHeaders...)
	sort.Strings(headers)
	for _, h := range headers {
		var v string
		if h == "host" {
			v = r.Host
		} else {
			v = strings.Join(r.Header.Values(h), ",")
		}
		b.WriteString(h + ":" + strings.Join(strings.Fields(v), " ") + "\n")
	}
	b.WriteString("\n" + strings.Join(headers, ";") + "\n")
	b.WriteString(s.payloadHash)
	return b.String()
}

func canonicalQuery(q url.Values, presigned bool) string {
	var pairs []string
	for k, vs := range q {
		if presigned && k == "X-Amz-Signature" {
			continue
		}
		for _, v := range vs {
			pairs = append(pairs, uriEncode(k, true)+"="+uriEncode(v, true))
		}
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "&")
}

// uriEncode is SigV4's UriEncode: every byte but the unreserved characters
// as %XX, in capitals, and '/' kept unless it's in a query.
func uriEncode(s string, encodeSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9', c == '-', c == '.', c == '_', c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func signature(secret string, date time.Time, scope, canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	toSign := algorithm + "\n" + date.Format(amzDateFormat) + "\n" + scope + "\n" + hex.EncodeToString(sum[:])
	parts := strings.Split(scope, "/")
	key := []byte("AWS4" + secret)
	for _, p := range parts {
		key = hmacSHA256(key, p)
	}
	return hex.EncodeToString(hmacSHA256(key, toSign))
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
