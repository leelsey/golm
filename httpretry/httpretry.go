// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

// Package httpretry adds bounded, backoff retries to net/http calls for the provider adapters.
package httpretry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Policy controls retry behaviour.
type Policy struct {
	Attempts int
	Base     time.Duration
	Cap      time.Duration
}

// Default is the standard policy used by providers unless overridden.
func Default() Policy {
	return Policy{Attempts: 3, Base: 500 * time.Millisecond, Cap: 10 * time.Second}
}

// Stats reports what a call cost beyond the attempt whose response is returned.
type Stats struct {
	Attempts int

	LostCompletions int
}

// Do issues the request built by mkReq, retrying network errors, 429 and 5xx with jittered backoff.
func Do(ctx context.Context, client *http.Client, p Policy, mkReq func() (*http.Request, error)) (*http.Response, error) {
	resp, _, err := DoStats(ctx, client, p, mkReq)
	return resp, err
}

// DoStats is Do, additionally reporting what the retries cost.
func DoStats(ctx context.Context, client *http.Client, p Policy, mkReq func() (*http.Request, error)) (*http.Response, Stats, error) {
	attempts := p.Attempts
	if attempts < 1 {
		attempts = 1
	}
	var resp *http.Response
	var err error
	var st Stats
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			d := backoff(p, attempt, resp)
			if resp != nil {
				DrainClose(resp.Body)
				resp = nil
			}
			t := time.NewTimer(d)
			select {
			case <-t.C:
			case <-ctx.Done():
				t.Stop()
				return nil, st, ctx.Err()
			}
		}
		var req *http.Request
		if req, err = mkReq(); err != nil {
			return nil, st, err
		}
		st.Attempts++
		resp, err = client.Do(req)
		if err != nil {
			if attempt < attempts-1 && reachedServer(err) {
				st.LostCompletions++
			}
			resp = nil
			continue
		}
		if !retryableStatus(resp.StatusCode) {
			return resp, st, nil
		}

		if attempt < attempts-1 && billedGatewayFailure(resp.StatusCode) {
			st.LostCompletions++
		}
	}

	return resp, st, err
}

func reachedServer(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return false
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return false
	}
	var recErr tls.RecordHeaderError
	if errors.As(err, &recErr) {
		return false
	}
	var hostErr x509.HostnameError
	if errors.As(err, &hostErr) {
		return false
	}
	var authErr x509.UnknownAuthorityError
	if errors.As(err, &authErr) {
		return false
	}
	var invErr x509.CertificateInvalidError
	if errors.As(err, &invErr) {
		return false
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && (opErr.Op == "dial" || opErr.Op == "proxyconnect") {
		return false
	}
	return true
}

func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

func billedGatewayFailure(code int) bool {
	return code == http.StatusBadGateway || code == http.StatusGatewayTimeout
}

const maxRetryAfter = 2 * time.Minute

const drainLimit = 64 << 10

// DrainClose drains a bounded amount of rc and closes it.
func DrainClose(rc io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(rc, drainLimit))
	rc.Close()
}

func backoff(p Policy, attempt int, resp *http.Response) time.Duration {
	if resp != nil {
		if ra := retryAfter(resp); ra > 0 {
			if ra > maxRetryAfter {
				ra = maxRetryAfter
			}
			return ra
		}
	}
	base := p.Base
	if base <= 0 {
		base = 500 * time.Millisecond
	}
	maxD := p.Cap
	if maxD <= 0 {
		maxD = 30 * time.Second
	}

	d := maxD
	if shift := attempt - 1; shift >= 0 && shift < 31 {
		if grown := base * time.Duration(int64(1)<<uint(shift)); grown > 0 && grown < maxD {
			d = grown
		}
	}
	if d <= 0 {
		d = base
	}
	return time.Duration(rand.Int63n(int64(d) + 1))
}

func retryAfter(resp *http.Response) time.Duration {
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0
	}
	if secs, err := strconv.ParseInt(v, 10, 64); err == nil && secs >= 0 {
		if maxSecs := int64(maxRetryAfter / time.Second); secs > maxSecs {
			return maxRetryAfter
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
