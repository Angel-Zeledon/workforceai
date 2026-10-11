package objstore

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// S3Config configures the S3-compatible store. Credentials come from the
// backend environment (or the vault): they never reach the runtime, prompts,
// the frontend or logs.
type S3Config struct {
	Endpoint     string // https://s3.amazonaws.com, https://<acct>.r2.cloudflarestorage.com, http://minio:9000
	Bucket       string
	AccessKey    string
	SecretKey    string
	Region       string // default "us-east-1" ("auto" for R2)
	UsePathStyle bool   // http://host/bucket/key (MinIO, R2); false = http://bucket.host/key
	// Prefix is prepended to every key inside the bucket (optional).
	Prefix string
	// HTTP is the client (default: 60 s timeout per request, no redirects followed).
	HTTP *http.Client
	Now  func() time.Time
}

// S3 is a minimal S3 client: Put, Get, Head, Delete and CreateBucket with AWS
// Signature V4 (header auth). It is hand written on purpose: aws-sdk-go-v2
// pulls dozens of modules and minio-go several transitive dependencies for the
// four calls the drive needs, and every new dependency is supply-chain surface
// in the component that holds the object-store credentials. The signer is
// checked against the published AWS test vector and, in CI/dev, against MinIO.
type S3 struct {
	cfg  S3Config
	base *url.URL
}

var _ Store = (*S3)(nil)

// NewS3 validates the configuration.
func NewS3(cfg S3Config) (*S3, error) {
	u, err := url.Parse(strings.TrimRight(cfg.Endpoint, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("objstore: S3 endpoint must be an http(s) URL")
	}
	if cfg.Bucket == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("objstore: S3 bucket, access key and secret key are required")
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 60 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	cfg.Prefix = strings.Trim(cfg.Prefix, "/")
	return &S3{cfg: cfg, base: u}, nil
}

func (s *S3) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now().UTC()
	}
	return time.Now().UTC()
}

// target returns the request URL (escaped exactly as it is signed) and Host.
func (s *S3) target(key string) (*url.URL, string) {
	path := ""
	if key != "" {
		k := key
		if s.cfg.Prefix != "" {
			k = s.cfg.Prefix + "/" + key
		}
		path = "/" + k
	}
	host := s.base.Host
	if s.cfg.UsePathStyle {
		path = "/" + s.cfg.Bucket + path
	} else {
		host = s.cfg.Bucket + "." + host
	}
	if path == "" {
		path = "/"
	}
	enc := uriEncode(path, false)
	u := &url.URL{Scheme: s.base.Scheme, Host: host, Path: path, RawPath: enc}
	return u, host
}

const unsignedPayload = "UNSIGNED-PAYLOAD"

// Put implements Store. The body is streamed with Content-Length; when the
// caller knows the digest the server verifies it (signed payload), otherwise
// the payload is unsigned and the returned SHA256 is computed while streaming.
func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64, o PutOptions) (Info, error) {
	if err := ValidKey(key); err != nil {
		return Info{}, err
	}
	var body io.Reader = r
	if size < 0 { // unknown length: S3 needs it up front
		max := o.MaxBytes
		if max <= 0 {
			max = 256 << 20
		}
		b, err := ReadAllBounded(r, max)
		if err != nil {
			return Info{}, err
		}
		size, body = int64(len(b)), bytes.NewReader(b)
		if o.SHA256 == "" {
			o.SHA256 = SumBytes(b)
		}
	}
	if o.MaxBytes > 0 && size > o.MaxBytes {
		return Info{}, ErrTooLarge
	}
	hr := NewHashingReader(body)
	payload := unsignedPayload
	if o.SHA256 != "" {
		payload = strings.ToLower(o.SHA256)
	}
	u, host := s.target(key)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), io.LimitReader(hr, size))
	if err != nil {
		return Info{}, err
	}
	req.ContentLength = size
	h := map[string]string{}
	if o.ContentType != "" {
		h["content-type"] = o.ContentType
	}
	if o.SHA256 != "" {
		h["x-amz-meta-sha256"] = payload
	}
	s.sign(req, host, h, payload)
	resp, err := s.cfg.HTTP.Do(req)
	if err != nil {
		return Info{}, fmt.Errorf("objstore: put: %w", errNoSecret(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return Info{}, s.statusErr("put", resp)
	}
	if hr.N != size {
		return Info{}, io.ErrUnexpectedEOF
	}
	return Info{Key: key, Size: size, SHA256: hr.Sum(), ContentType: o.ContentType, ModifiedAt: s.now()}, nil
}

// Get implements Store.
func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, Info, error) {
	if err := ValidKey(key); err != nil {
		return nil, Info{}, err
	}
	resp, err := s.do(ctx, http.MethodGet, key)
	if err != nil {
		return nil, Info{}, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		return nil, Info{}, s.statusErr("get", resp)
	}
	return resp.Body, infoOf(key, resp), nil
}

// Head implements Store.
func (s *S3) Head(ctx context.Context, key string) (Info, error) {
	if err := ValidKey(key); err != nil {
		return Info{}, err
	}
	resp, err := s.do(ctx, http.MethodHead, key)
	if err != nil {
		return Info{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return Info{}, s.statusErr("head", resp)
	}
	return infoOf(key, resp), nil
}

// Delete implements Store (a missing key is not an error: S3 answers 204).
func (s *S3) Delete(ctx context.Context, key string) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	resp, err := s.do(ctx, http.MethodDelete, key)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusNotFound {
		return s.statusErr("delete", resp)
	}
	return nil
}

// CreateBucket creates the bucket (idempotent). Dev and tests; production
// buckets are provisioned by infrastructure.
func (s *S3) CreateBucket(ctx context.Context) error {
	u, host := s.base, s.base.Host
	path := "/" + s.cfg.Bucket
	bu := &url.URL{Scheme: u.Scheme, Host: host, Path: path, RawPath: uriEncode(path, false)}
	if !s.cfg.UsePathStyle {
		bu = &url.URL{Scheme: u.Scheme, Host: s.cfg.Bucket + "." + host, Path: "/"}
		host = bu.Host
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, bu.String(), nil)
	if err != nil {
		return err
	}
	s.sign(req, host, nil, emptySHA)
	resp, err := s.cfg.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("objstore: create bucket: %w", errNoSecret(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 2 || resp.StatusCode == http.StatusConflict {
		return nil // 409: BucketAlreadyOwnedByYou
	}
	return s.statusErr("create bucket", resp)
}

func (s *S3) do(ctx context.Context, method, key string) (*http.Response, error) {
	u, host := s.target(key)
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, err
	}
	s.sign(req, host, nil, emptySHA)
	resp, err := s.cfg.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("objstore: %s: %w", strings.ToLower(method), errNoSecret(err))
	}
	return resp, nil
}

func infoOf(key string, resp *http.Response) Info {
	i := Info{Key: key, Size: resp.ContentLength, ContentType: resp.Header.Get("Content-Type"), SHA256: resp.Header.Get("x-amz-meta-sha256")}
	if t, err := http.ParseTime(resp.Header.Get("Last-Modified")); err == nil {
		i.ModifiedAt = t
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		if n, err := strconv.ParseInt(cl, 10, 64); err == nil {
			i.Size = n
		}
	}
	return i
}

func (s *S3) statusErr(op string, resp *http.Response) error {
	if resp.StatusCode == http.StatusNotFound {
		// NoSuchKey; a missing bucket is also 404 but with another code.
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		if bytes.Contains(b, []byte("NoSuchBucket")) {
			return fmt.Errorf("objstore: %s: bucket %q does not exist", op, s.cfg.Bucket)
		}
		return ErrNotFound
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	code := xmlTag(string(b), "Code")
	return fmt.Errorf("objstore: %s: HTTP %d %s", op, resp.StatusCode, code)
}

func xmlTag(s, tag string) string {
	i := strings.Index(s, "<"+tag+">")
	j := strings.Index(s, "</"+tag+">")
	if i < 0 || j < i {
		return ""
	}
	v := s[i+len(tag)+2 : j]
	if len(v) > 64 {
		v = v[:64]
	}
	return v
}

// errNoSecret unwraps url.Error so the (signed) URL is not echoed; it holds no
// secret, but keeps log lines short and free of query strings.
func errNoSecret(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// ---- AWS Signature Version 4 ----

const emptySHA = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// sign adds the SigV4 Authorization header. extra are additional headers to
// sign (lower-case names).
func (s *S3) sign(req *http.Request, host string, extra map[string]string, payloadHash string) {
	t := s.now()
	signV4(req, host, extra, payloadHash, s.cfg.AccessKey, s.cfg.SecretKey, s.cfg.Region, t)
}

func signV4(req *http.Request, host string, extra map[string]string, payloadHash, access, secret, region string, t time.Time) {
	amzDate := t.Format("20060102T150405Z")
	day := t.Format("20060102")
	hdr := map[string]string{"host": host, "x-amz-content-sha256": payloadHash, "x-amz-date": amzDate}
	for k, v := range extra {
		hdr[strings.ToLower(k)] = strings.TrimSpace(v)
	}
	names := make([]string, 0, len(hdr))
	for k := range hdr {
		names = append(names, k)
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, k := range names {
		canonHeaders.WriteString(k + ":" + hdr[k] + "\n")
	}
	signed := strings.Join(names, ";")
	canonURI := req.URL.EscapedPath()
	if canonURI == "" {
		canonURI = "/"
	}
	canonical := strings.Join([]string{req.Method, canonURI, canonicalQuery(req.URL.Query()), canonHeaders.String(), signed, payloadHash}, "\n")
	scope := day + "/" + region + "/s3/aws4_request"
	sts := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hexSHA([]byte(canonical))
	k := hmacSHA([]byte("AWS4"+secret), day)
	k = hmacSHA(k, region)
	k = hmacSHA(k, "s3")
	k = hmacSHA(k, "aws4_request")
	sig := hex.EncodeToString(hmacSHA(k, sts))

	req.Host = host
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	req.Header.Set("Authorization", fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s", access, scope, signed, sig))
}

func canonicalQuery(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	var parts []string
	for k, vs := range q {
		for _, v := range vs {
			parts = append(parts, uriEncode(k, true)+"="+uriEncode(v, true))
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "&")
}

func hexSHA(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func hmacSHA(key []byte, msg string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return m.Sum(nil)
}

// uriEncode percent-encodes everything but the RFC 3986 unreserved characters
// (and "/" when encodeSlash is false), as SigV4 requires.
func uriEncode(s string, encodeSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
