// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package httpretry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func mk(url string) func() (*http.Request, error) {
	return func() (*http.Request, error) { return http.NewRequest(http.MethodPost, url, nil) }
}

func TestRetriesOn429ThenSucceeds(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	p := Policy{Attempts: 3, Base: time.Millisecond, Cap: 5 * time.Millisecond}
	resp, err := Do(context.Background(), srv.Client(), p, mk(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&n); got != 2 {
		t.Fatalf("attempts=%d, want 2", got)
	}
}

func TestNoRetryOn400(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	resp, err := Do(context.Background(), srv.Client(), Policy{Attempts: 3, Base: time.Millisecond}, mk(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := atomic.LoadInt32(&n); got != 1 {
		t.Fatalf("attempts=%d, want 1 (400 not retryable)", got)
	}
}

func TestDisabledPolicySingleAttempt(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	resp, err := Do(context.Background(), srv.Client(), Policy{}, mk(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := atomic.LoadInt32(&n); got != 1 {
		t.Fatalf("attempts=%d, want 1", got)
	}
}

func TestRetryAfterParsing(t *testing.T) {
	mkResp := func(v string) *http.Response {
		h := http.Header{}
		if v != "" {
			h.Set("Retry-After", v)
		}
		return &http.Response{Header: h}
	}
	if d := retryAfter(mkResp("2")); d != 2*time.Second {
		t.Errorf("seconds: got %v", d)
	}
	if d := retryAfter(mkResp("")); d != 0 {
		t.Errorf("empty: got %v", d)
	}
	future := time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat)
	if d := retryAfter(mkResp(future)); d <= 0 || d > 4*time.Second {
		t.Errorf("http-date: got %v", d)
	}
}
