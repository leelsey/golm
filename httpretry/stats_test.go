// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package httpretry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func fastPolicy() Policy {
	return Policy{Attempts: 3, Base: time.Millisecond, Cap: 2 * time.Millisecond}
}

// A completion the server produced and charged for, whose response never got back.
func TestDoStats_CountsAttemptsLostAfterTheRequestWasDelivered(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) < 3 {
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("test server does not support hijacking")
				return
			}
			c, _, err := hj.Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			c.Close()
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp, st, err := DoStats(context.Background(), srv.Client(), fastPolicy(), mk(srv.URL))
	if err != nil {
		t.Fatalf("DoStats: %v", err)
	}
	defer DrainClose(resp.Body)

	if st.Attempts != 3 {
		t.Errorf("Attempts=%d want 3", st.Attempts)
	}
	if st.LostCompletions != 2 {
		t.Errorf("LostCompletions=%d want 2 — both delivered requests may have been billed", st.LostCompletions)
	}
}

// The final attempt is the outcome this call reports.
func TestDoStats_DoesNotCountTheAttemptItReports(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("test server does not support hijacking")
			return
		}
		c, _, err := hj.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		c.Close()
	}))
	defer srv.Close()

	_, st, err := DoStats(context.Background(), srv.Client(), fastPolicy(), mk(srv.URL))
	if err == nil {
		t.Fatal("expected every attempt to fail")
	}
	if st.Attempts != 3 {
		t.Fatalf("Attempts=%d want 3", st.Attempts)
	}
	if st.LostCompletions != 2 {
		t.Errorf("LostCompletions=%d want 2 — three requests, one of which the caller books itself", st.LostCompletions)
	}
}

// A deadline we set is not a lost completion.
func TestDoStats_DoesNotCountOurOwnCancellation(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	mkCtx := func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, nil)
	}
	_, st, err := DoStats(ctx, srv.Client(), fastPolicy(), mkCtx)
	if err == nil {
		t.Fatal("expected the deadline to surface")
	}
	if st.LostCompletions != 0 {
		t.Errorf("LostCompletions=%d want 0 — the caller already accounts for what it cancelled", st.LostCompletions)
	}
}

// A dial that never connected sent nothing.
func TestDoStats_DoesNotCountAnAttemptThatNeverConnected(t *testing.T) {
	_, st, err := DoStats(context.Background(), &http.Client{}, fastPolicy(), mk("http://127.0.0.1:0/"))
	if err == nil {
		t.Fatal("expected the dial to fail")
	}
	if st.Attempts == 0 {
		t.Fatal("Attempts=0 — nothing was tried")
	}
	if st.LostCompletions != 0 {
		t.Errorf("LostCompletions=%d want 0 for a connection that was never established", st.LostCompletions)
	}
}

// A 429 or 5xx arrived. The provider rejected the request rather than serving it.
func TestDoStats_DoesNotCountRetriedRejections(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp, st, err := DoStats(context.Background(), srv.Client(), fastPolicy(), mk(srv.URL))
	if err != nil {
		t.Fatalf("DoStats: %v", err)
	}
	defer DrainClose(resp.Body)
	if st.LostCompletions != 0 {
		t.Errorf("LostCompletions=%d want 0 — a rejected request is not a billed one", st.LostCompletions)
	}
}

func TestReachedServer(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"dns", &net.DNSError{Err: "no such host"}, false},
		{"dial", &net.OpError{Op: "dial", Err: errors.New("refused")}, false},
		{"read after send", &net.OpError{Op: "read", Err: errors.New("reset")}, true},
		{"unclassified", errors.New("http2: server sent GOAWAY"), true},

		{"ctx cancel", context.Canceled, false},
		{"ctx deadline", context.DeadlineExceeded, false},
		{"wrapped ctx deadline", &url.Error{Op: "Post", Err: context.DeadlineExceeded}, false},
		{"untrusted cert", x509.UnknownAuthorityError{}, false},
		{"wrong hostname", x509.HostnameError{Host: "example.invalid"}, false},
		{"cert invalid", x509.CertificateInvalidError{Reason: x509.Expired}, false},
		{"tls verification", &tls.CertificateVerificationError{}, false},
		{"proxy connect", &net.OpError{Op: "proxyconnect", Err: errors.New("refused")}, false},
	}
	for _, c := range cases {
		if got := reachedServer(c.err); got != c.want {
			t.Errorf("%s: reachedServer=%v want %v", c.name, got, c.want)
		}
	}
}

// Do is the compatibility wrapper.
func TestDo_StillReturnsTwoValues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	resp, err := Do(context.Background(), srv.Client(), fastPolicy(), mk(srv.URL))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	DrainClose(resp.Body)
}

// A gateway error is not a rejection.
func TestDoStats_CountsRetriedGatewayFailures(t *testing.T) {
	for _, code := range []int{http.StatusBadGateway, http.StatusGatewayTimeout} {
		var n int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if atomic.AddInt32(&n, 1) < 3 {
				w.WriteHeader(code)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		resp, st, err := DoStats(context.Background(), srv.Client(), fastPolicy(), mk(srv.URL))
		if err != nil {
			t.Fatalf("%d: DoStats: %v", code, err)
		}
		DrainClose(resp.Body)
		srv.Close()
		if st.LostCompletions != 2 {
			t.Errorf("%d: LostCompletions=%d want 2 — two generations were paid for and discarded", code, st.LostCompletions)
		}
	}
}

// 503 and 529 stay uncounted.
func TestDoStats_StillIgnoresRefusals(t *testing.T) {
	for _, code := range []int{http.StatusServiceUnavailable, 529} {
		var n int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if atomic.AddInt32(&n, 1) < 3 {
				w.WriteHeader(code)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		resp, st, err := DoStats(context.Background(), srv.Client(), fastPolicy(), mk(srv.URL))
		if err != nil {
			t.Fatalf("%d: DoStats: %v", code, err)
		}
		DrainClose(resp.Body)
		srv.Close()
		if st.LostCompletions != 0 {
			t.Errorf("%d: LostCompletions=%d want 0 — a refusal is not a billed completion", code, st.LostCompletions)
		}
	}
}
