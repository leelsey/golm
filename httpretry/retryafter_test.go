// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package httpretry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestRetryAfterClampsHugeValue(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", "18446744074")
	if d := retryAfter(resp); d != maxRetryAfter {
		t.Errorf("retryAfter = %v, want clamp to %v", d, maxRetryAfter)
	}
}

func TestRetryReusesConnection(t *testing.T) {
	var mu sync.Mutex
	addrs := map[string]bool{}
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		addrs[r.RemoteAddr] = true
		n++
		first := n == 1
		mu.Unlock()
		if first {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, "slow down")
			return
		}
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "ok")
	}))
	defer srv.Close()

	resp, err := Do(context.Background(), srv.Client(),
		Policy{Attempts: 3, Base: time.Millisecond, Cap: time.Millisecond},
		func() (*http.Request, error) { return http.NewRequest(http.MethodGet, srv.URL, nil) })
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	mu.Lock()
	defer mu.Unlock()
	if len(addrs) != 1 {
		t.Errorf("retry used %d connections, want 1 (prior body not drained for keep-alive)", len(addrs))
	}
}
