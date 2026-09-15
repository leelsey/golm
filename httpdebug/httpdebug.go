// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

// Package httpdebug provides an http.RoundTripper that dumps each request.
package httpdebug

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/leelsey/golm"
)

// DefaultMaxBody bounds how much of each body is written to the log.
const DefaultMaxBody = 64 << 10

// Transport dumps requests and responses to W.
type Transport struct {
	Base    http.RoundTripper
	W       io.Writer
	MaxBody int

	mu sync.Mutex
	n  atomic.Int64
}

// NewClient returns an *http.Client whose traffic is dumped to w.
func NewClient(w io.Writer) *http.Client {
	return &http.Client{Transport: &Transport{W: w}}
}

func credentialsIn(h http.Header) []string {
	var out []string
	for name := range redactedHeaders {
		for _, v := range h.Values(name) {
			if v == "" {
				continue
			}
			out = append(out, v)
			if token, ok := strings.CutPrefix(v, "Bearer "); ok && token != "" {
				out = append(out, token)
			}
		}
	}
	return out
}

var redactedHeaders = map[string]bool{
	"Authorization":       true,
	"Proxy-Authorization": true,
	"Cookie":              true,
	"Set-Cookie":          true,
	"X-Api-Key":           true,
	"X-Goog-Api-Key":      true,
}

func (t *Transport) base() http.RoundTripper {
	if t.Base != nil {
		return t.Base
	}
	return http.DefaultTransport
}

func (t *Transport) maxBody() int {
	if t.MaxBody > 0 {
		return t.MaxBody
	}
	return DefaultMaxBody
}

func (t *Transport) write(p []byte) {
	w := t.W
	if w == nil {
		w = os.Stderr
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	_, _ = w.Write(p)
}

func (t *Transport) logf(format string, args ...any) {
	t.write([]byte(fmt.Sprintf(format+"\n", args...)))
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	id := t.n.Add(1)
	req, body, err := cloneWithBody(req)
	if err != nil {
		return nil, fmt.Errorf("httpdebug: read request body: %w", err)
	}

	secrets := credentialsIn(req.Header)

	t.logf(">> [%d] %s %s", id, req.Method, redactURL(req.URL))
	t.logHeaders(">>", id, req.Header)
	if len(body) > 0 {
		t.logf(">> [%d] body (%d bytes): %s", id, len(body), capped(body, t.maxBody()))
	}

	start := time.Now()
	resp, err := t.base().RoundTrip(req)
	if err != nil {
		t.logf("<< [%d] error after %s: %v", id, time.Since(start).Round(time.Millisecond), err)
		return nil, err
	}
	t.logf("<< [%d] %s (%s)", id, resp.Status, time.Since(start).Round(time.Millisecond))
	t.logHeaders("<<", id, resp.Header)
	if resp.Body != nil {
		t.logf("<< [%d] body:", id)
		resp.Body = &teeBody{rc: resp.Body, t: t, id: id, remaining: t.maxBody(), secrets: secrets}
	}
	return resp, nil
}

func (t *Transport) logHeaders(dir string, id int64, h http.Header) {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := strings.Join(h[k], ", ")
		if redactedHeaders[http.CanonicalHeaderKey(k)] {
			v = "[redacted]"
		}
		t.logf("%s [%d] %s: %s", dir, id, k, v)
	}
}

func cloneWithBody(req *http.Request) (*http.Request, []byte, error) {
	clone := req.Clone(req.Context())
	if req.Body == nil {
		return clone, nil, nil
	}
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, nil, err
	}
	clone.Body = io.NopCloser(bytes.NewReader(body))
	return clone, body, nil
}

func redactURL(u *url.URL) string {
	r := *u
	if r.User != nil {
		r.User = url.User("redacted")
	}
	q := r.Query()
	changed := false
	for k := range q {
		if l := strings.ToLower(k); strings.Contains(l, "key") || strings.Contains(l, "token") {
			q.Set(k, "redacted")
			changed = true
		}
	}
	if changed {
		r.RawQuery = q.Encode()
	}
	return r.String()
}

func capped(b []byte, max int) string {
	if len(b) > max {
		return string(b[:max]) + fmt.Sprintf(" …(capped, %d bytes total)", len(b))
	}
	return string(b)
}

type teeBody struct {
	rc        io.ReadCloser
	t         *Transport
	id        int64
	remaining int

	secrets []string
	total   atomic.Int64
	done    atomic.Bool
}

func (b *teeBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 {
		b.total.Add(int64(n))
		if b.remaining > 0 {
			chunk := p[:n]
			if len(chunk) > b.remaining {
				chunk = chunk[:b.remaining]
			}
			b.remaining -= len(chunk)
			b.t.write([]byte(golm.Redact(string(chunk), b.secrets...)))
			if b.remaining == 0 {
				b.t.logf("\n<< [%d] body log capped", b.id)
			}
		}
	}
	if err == io.EOF {
		b.finish()
	}
	return n, err
}

func (b *teeBody) Close() error {
	b.finish()
	return b.rc.Close()
}

func (b *teeBody) finish() {
	if !b.done.CompareAndSwap(false, true) {
		return
	}
	b.t.logf("\n<< [%d] body end (%d bytes)", b.id, b.total.Load())
}
