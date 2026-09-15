// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package openai

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

func TestClientRetriesOn503(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{}}`))
	}))
	defer srv.Close()
	c := New("k").WithBaseURL(srv.URL).WithRetry(httpretry.Policy{Attempts: 3, Base: time.Millisecond, Cap: 5 * time.Millisecond})
	resp, err := c.Complete(context.Background(), golm.Request{Model: "gpt-4o", Messages: []golm.Message{golm.UserText("hi")}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Message.Text() != "ok" {
		t.Errorf("got %q, want ok", resp.Message.Text())
	}
	if got := atomic.LoadInt32(&n); got != 2 {
		t.Errorf("attempts=%d, want 2", got)
	}
}

func TestClientWithoutRetryFailsFast(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := New("k").WithBaseURL(srv.URL).WithoutRetry()
	if _, err := c.Complete(context.Background(), golm.Request{Model: "gpt-4o", Messages: []golm.Message{golm.UserText("hi")}}); err == nil {
		t.Fatal("expected error from 503")
	}
	if got := atomic.LoadInt32(&n); got != 1 {
		t.Errorf("attempts=%d, want 1", got)
	}
}
