// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package google

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/httpretry"
)

const okBody = `{"candidates":[{"content":{"parts":[{"text":"ok"}],"role":"model"},` +
	`"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}`

// The same seam openai's retry test covers.
func TestClientRetriesOn503(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(okBody))
	}))
	defer srv.Close()

	c := New("k").WithBaseURL(srv.URL).
		WithRetry(httpretry.Policy{Attempts: 3, Base: time.Millisecond, Cap: 5 * time.Millisecond})
	resp, err := c.Complete(context.Background(),
		golm.Request{Model: "gemini-2.5-flash", Messages: []golm.Message{golm.UserText("hi")}, MaxTokens: 16})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Message.Text() != "ok" {
		t.Errorf("got %q, want ok", resp.Message.Text())
	}
	if got := atomic.LoadInt32(&n); got != 2 {
		t.Errorf("attempts = %d, want 2", got)
	}
}

type countingTransport struct {
	n    atomic.Int32
	next http.RoundTripper
}

func (t *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.n.Add(1)
	return t.next.RoundTrip(r)
}

func TestWithHTTPClientIsTheOneUsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(okBody))
	}))
	defer srv.Close()

	tr := &countingTransport{next: http.DefaultTransport}
	c := New("k").WithBaseURL(srv.URL).WithHTTPClient(&http.Client{Transport: tr})
	if _, err := c.Complete(context.Background(),
		golm.Request{Model: "gemini-2.5-flash", Messages: []golm.Message{golm.UserText("hi")}, MaxTokens: 16}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got := tr.n.Load(); got != 1 {
		t.Errorf("the supplied client made %d request(s), want 1 — it was stored but not used", got)
	}
}
