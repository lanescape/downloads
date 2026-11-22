// Package r2 emulates what the service and its tests use of Cloudflare R2's
// S3 API (specs/0001-service.md, Emulators): path-style GET, HEAD and PUT of
// objects at /<bucket>/<key>, signed with AWS Signature Version 4 in an
// Authorization header or a presigned URL's query, with R2's region, auto.
//
// Like R2, it checks every signature, refuses a presigned URL once it has
// expired, gives each API token access to its own buckets, read-only or
// read and write, and answers conditional writes, If-Match and
// If-None-Match, atomically, with 412 PreconditionFailed when one fails. It
// stores Content-Type, Cache-Control and x-amz-meta-* with an object and
// gives them back, with a quoted ETag, the MD5 of the object as R2 gives
// for a single upload. It doesn't list buckets, take multipart uploads or
// answer range or conditional reads, which nothing here uses.
package r2

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Access is what a token may do in its buckets.
type Access int

const (
	// Read lets a token GET and HEAD objects: R2's Object Read.
	Read Access = iota + 1
	// ReadWrite lets it PUT them too: R2's Object Read & Write.
	ReadWrite
)

// Object is a stored object.
type Object struct {
	Body         []byte
	ETag         string // quoted, as R2 gives it
	ContentType  string
	CacheControl string
	Meta         map[string]string // x-amz-meta-*, by lowercase name without the prefix
	Modified     time.Time
}

type token struct {
	secret  string
	buckets map[string]Access
}

// Emulator is an R2 account's S3 API.
type Emulator struct {
	// Now is the emulator's clock, time.Now unless set.
	Now func() time.Time

	mu      sync.Mutex
	tokens  map[string]token
	buckets map[string]map[string]*Object
}

// New is an emulator with no buckets or tokens.
func New() *Emulator {
	return &Emulator{tokens: map[string]token{}, buckets: map[string]map[string]*Object{}}
}

// AddBucket makes an empty bucket.
func (e *Emulator) AddBucket(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.buckets[name] == nil {
		e.buckets[name] = map[string]*Object{}
	}
}

// AddToken makes an API token, whose access key ID and secret access key
// sign requests, with access to the buckets named.
func (e *Emulator) AddToken(accessKeyID, secretAccessKey string, access Access, buckets ...string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t := token{secret: secretAccessKey, buckets: map[string]Access{}}
	for _, b := range buckets {
		t.buckets[b] = access
	}
	e.tokens[accessKeyID] = t
}

// Put stores an object directly, as a test's setup, making its bucket if
// there's none.
func (e *Emulator) Put(bucket, key string, body []byte, contentType string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.buckets[bucket] == nil {
		e.buckets[bucket] = map[string]*Object{}
	}
	e.store(bucket, key, body, contentType, "", nil)
}

// Get is a stored object, for a test to inspect, or nil.
func (e *Emulator) Get(bucket, key string) *Object {
	e.mu.Lock()
	defer e.mu.Unlock()
	o := e.buckets[bucket][key]
	if o == nil {
		return nil
	}
	c := *o
	return &c
}

func (e *Emulator) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Emulator) store(bucket, key string, body []byte, contentType, cacheControl string, meta map[string]string) *Object {
	sum := md5.Sum(body)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	o := &Object{
		Body:         append([]byte(nil), body...),
		ETag:         `"` + hex.EncodeToString(sum[:]) + `"`,
		ContentType:  contentType,
		CacheControl: cacheControl,
		Meta:         meta,
		Modified:     e.now().UTC().Truncate(time.Second),
	}
	e.buckets[bucket][key] = o
	return o
}

// ServeHTTP answers an S3 API request.
func (e *Emulator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	bucket, key, _ := strings.Cut(path, "/")
	if bucket == "" || key == "" {
		e.fail(w, r, &authError{http.StatusNotImplemented, "NotImplemented", "only objects are emulated"})
		return
	}
	var need Access
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		need = Read
	case http.MethodPut:
		need = ReadWrite
	default:
		e.fail(w, r, &authError{http.StatusNotImplemented, "NotImplemented", r.Method + " isn't emulated"})
		return
	}
	s, err := parse(r)
	if err != nil {
		e.fail(w, r, err)
		return
	}
	e.mu.Lock()
	t, ok := e.tokens[s.accessKeyID]
	e.mu.Unlock()
	if !ok {
		e.fail(w, r, refuse(http.StatusForbidden, "InvalidAccessKeyId", "the access key ID doesn't exist"))
		return
	}
	if err := s.verify(r, t.secret, e.now()); err != nil {
		e.fail(w, r, err)
		return
	}
	if t.buckets[bucket] < need {
		e.fail(w, r, refuse(http.StatusForbidden, "AccessDenied", "Access Denied"))
		return
	}
	if r.Method == http.MethodPut {
		e.put(w, r, s, bucket, key)
		return
	}
	e.mu.Lock()
	objects, ok := e.buckets[bucket]
	var o *Object
	if ok {
		o = objects[key]
	}
	e.mu.Unlock()
	switch {
	case !ok:
		e.fail(w, r, refuse(http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist."))
	case o == nil:
		e.fail(w, r, refuse(http.StatusNotFound, "NoSuchKey", "The specified key does not exist."))
	default:
		h := w.Header()
		writeHeaders(h, o)
		h.Set("Content-Length", strconv.Itoa(len(o.Body)))
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(o.Body)
		}
	}
}

func writeHeaders(h http.Header, o *Object) {
	h.Set("ETag", o.ETag)
	h.Set("Content-Type", o.ContentType)
	h.Set("Last-Modified", o.Modified.Format(http.TimeFormat))
	if o.CacheControl != "" {
		h.Set("Cache-Control", o.CacheControl)
	}
	for k, v := range o.Meta {
		h.Set("X-Amz-Meta-"+k, v)
	}
}

func (e *Emulator) put(w http.ResponseWriter, r *http.Request, s *signed, bucket, key string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		e.fail(w, r, refuse(http.StatusBadRequest, "IncompleteBody", "couldn't read the body"))
		return
	}
	if s.payloadHash != unsignedPayload {
		if got := sha256Hex(body); got != s.payloadHash {
			e.fail(w, r, refuse(http.StatusBadRequest, "XAmzContentSHA256Mismatch", "the body's SHA-256 isn't x-amz-content-sha256"))
			return
		}
	}
	meta := map[string]string{}
	for k, vs := range r.Header {
		if name, ok := strings.CutPrefix(strings.ToLower(k), "x-amz-meta-"); ok && name != "" {
			meta[name] = strings.Join(vs, ",")
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	objects, ok := e.buckets[bucket]
	if !ok {
		e.fail(w, r, refuse(http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist."))
		return
	}
	// Evaluated against the stored object, under the lock that stores the
	// new one, as R2 evaluates them atomically.
	if !preconditions(r.Header, objects[key]) {
		e.fail(w, r, refuse(http.StatusPreconditionFailed, "PreconditionFailed", "At least one of the pre-conditions you specified did not hold"))
		return
	}
	o := e.store(bucket, key, body, r.Header.Get("Content-Type"), r.Header.Get("Cache-Control"), meta)
	w.Header().Set("ETag", o.ETag)
	w.WriteHeader(http.StatusOK)
}

// preconditions are R2's for a write: If-Match holds when the object exists
// and its ETag is one listed, or * is, compared strongly; If-None-Match
// holds when it doesn't exist, or its ETag is none listed and * isn't,
// compared weakly.
func preconditions(h http.Header, o *Object) bool {
	if v := h.Get("If-Match"); v != "" {
		if o == nil || !matches(v, o.ETag, false) {
			return false
		}
	}
	if v := h.Get("If-None-Match"); v != "" {
		if o != nil && matches(v, o.ETag, true) {
			return false
		}
	}
	return true
}

// matches is whether a list of ETags has etag, or *. Strongly, a weak ETag
// never matches; weakly, W/ is ignored on either side.
func matches(list, etag string, weak bool) bool {
	for v := range strings.SplitSeq(list, ",") {
		v = strings.TrimSpace(v)
		if v == "*" {
			return true
		}
		if weak {
			if strings.TrimPrefix(v, "W/") == strings.TrimPrefix(etag, "W/") {
				return true
			}
		} else if v == etag {
			return true
		}
	}
	return false
}

type xmlError struct {
	XMLName xml.Name `xml:"Error"`
	Code    string   `xml:"Code"`
	Message string   `xml:"Message"`
}

func (e *Emulator) fail(w http.ResponseWriter, r *http.Request, err *authError) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(err.status)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.WriteString(w, xml.Header)
	_ = xml.NewEncoder(w).Encode(xmlError{Code: err.code, Message: err.message})
}
