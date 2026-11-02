package backup

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// S3 is a minimal S3-compatible client (AWS SigV4) for archive upload, download and delete.
// It's native rather than the aws-cli helper container the database backups use because the
// control plane must test credentials itself, and a container agent's staging files aren't
// visible to other containers.
type S3 struct {
	Endpoint  string // e.g. https://<account>.r2.cloudflarestorage.com; empty means Amazon S3 in Region
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	Client    *http.Client
}

// ObjectKey is where a server's archive lives: <prefix>/<serverID>/<file>.
func ObjectKey(prefix, serverID, file string) string {
	return strings.Trim(strings.Trim(prefix, "/")+"/"+serverID+"/"+file, "/")
}

// singlePutMax: larger archives use multipart upload (S3 caps a single PUT at 5 GiB).
var singlePutMax int64 = 5 << 30

const partSize = 512 << 20

func (c S3) region() string {
	if c.Region == "" {
		return "us-east-1"
	}
	return c.Region
}

func (c S3) objectURL(key string) *url.URL {
	if c.Endpoint == "" {
		return &url.URL{Scheme: "https", Host: c.Bucket + ".s3." + c.region() + ".amazonaws.com", Path: "/" + key}
	}
	u, _ := url.Parse(strings.TrimSuffix(c.Endpoint, "/"))
	u.Path = "/" + c.Bucket + "/" + key // path-style: works on every S3-compatible provider
	return u
}

func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	esc := func(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }
	for _, k := range keys {
		parts = append(parts, esc(k)+"="+esc(q.Get(k)))
	}
	return strings.Join(parts, "&")
}

func hmacSHA(key []byte, s string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(s))
	return h.Sum(nil)
}

// sign adds SigV4 headers. The payload is UNSIGNED-PAYLOAD (allowed over TLS), so bodies stream.
func (c S3) sign(req *http.Request, now time.Time) {
	amzDate := now.UTC().Format("20060102T150405Z")
	day := amzDate[:8]
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", "UNSIGNED-PAYLOAD")
	const signed = "host;x-amz-content-sha256;x-amz-date"
	creq := strings.Join([]string{req.Method, req.URL.EscapedPath(), req.URL.RawQuery,
		"host:" + req.URL.Host + "\nx-amz-content-sha256:UNSIGNED-PAYLOAD\nx-amz-date:" + amzDate + "\n", signed, "UNSIGNED-PAYLOAD"}, "\n")
	scope := day + "/" + c.region() + "/s3/aws4_request"
	sum := sha256.Sum256([]byte(creq))
	k := hmacSHA([]byte("AWS4"+c.SecretKey), day)
	k = hmacSHA(k, c.region())
	k = hmacSHA(k, "s3")
	k = hmacSHA(k, "aws4_request")
	sig := hex.EncodeToString(hmacSHA(k, "AWS4-HMAC-SHA256\n"+amzDate+"\n"+scope+"\n"+hex.EncodeToString(sum[:])))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+c.AccessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
}

func (c S3) do(ctx context.Context, method, key string, q url.Values, body io.Reader, size int64) (*http.Response, error) {
	u := c.objectURL(key)
	u.RawQuery = canonicalQuery(q)
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.ContentLength = size
		if size == 0 {
			req.Body = http.NoBody
		}
	}
	c.sign(req, time.Now())
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		var e struct{ Code, Message string }
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		xml.Unmarshal(raw, &e)
		if e.Code == "" {
			e.Code = resp.Status
		}
		return nil, fmt.Errorf("S3 %s %s: %s %s", method, key, e.Code, e.Message)
	}
	return resp, nil
}

func (c S3) call(ctx context.Context, method, key string, q url.Values, body io.Reader, size int64, out any) (http.Header, error) {
	resp, err := c.do(ctx, method, key, q, body, size)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if out != nil {
		return resp.Header, xml.NewDecoder(resp.Body).Decode(out)
	}
	_, err = io.Copy(io.Discard, resp.Body)
	return resp.Header, err
}

// Upload stores size bytes of r at key, using multipart upload above singlePutMax.
func (c S3) Upload(ctx context.Context, key string, r io.ReaderAt, size int64) error {
	if size <= singlePutMax {
		_, err := c.call(ctx, "PUT", key, nil, io.NewSectionReader(r, 0, size), size, nil)
		return err
	}
	var init struct{ UploadId string }
	if _, err := c.call(ctx, "POST", key, url.Values{"uploads": {""}}, nil, 0, &init); err != nil {
		return err
	}
	type part struct {
		PartNumber int
		ETag       string
	}
	var parts []part
	for off, n := int64(0), 1; off < size; off, n = off+partSize, n+1 {
		ln := min(partSize, size-off)
		h, err := c.call(ctx, "PUT", key, url.Values{"partNumber": {strconv.Itoa(n)}, "uploadId": {init.UploadId}}, io.NewSectionReader(r, off, ln), ln, nil)
		if err != nil {
			c.call(context.WithoutCancel(ctx), "DELETE", key, url.Values{"uploadId": {init.UploadId}}, nil, 0, nil)
			return err
		}
		parts = append(parts, part{n, h.Get("ETag")})
	}
	body, _ := xml.Marshal(struct {
		XMLName xml.Name `xml:"CompleteMultipartUpload"`
		Part    []part
	}{Part: parts})
	// S3 can answer 200 with an error document here, so the response body is checked too.
	var done struct {
		XMLName xml.Name
		Code    string
		Message string
	}
	if _, err := c.call(ctx, "POST", key, url.Values{"uploadId": {init.UploadId}}, bytes.NewReader(body), int64(len(body)), &done); err != nil {
		return err
	}
	if done.XMLName.Local == "Error" {
		return fmt.Errorf("S3 complete upload %s: %s %s", key, done.Code, done.Message)
	}
	return nil
}

func (c S3) Download(ctx context.Context, key string, w io.Writer) error {
	resp, err := c.do(ctx, "GET", key, nil, nil, 0)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(w, resp.Body)
	return err
}

func (c S3) Delete(ctx context.Context, key string) error {
	_, err := c.call(ctx, "DELETE", key, nil, nil, 0, nil)
	return err
}
