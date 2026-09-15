// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package httpretry

import (
	"net/http"
	"testing"
	"time"
)

func TestBackoffHighAttemptNoPanic(t *testing.T) {
	for _, a := range []int{1, 30, 31, 40, 64, 100} {
		if d := backoff(Policy{Base: 500 * time.Millisecond, Cap: 10 * time.Second}, a, nil); d < 0 {
			t.Errorf("attempt %d: negative backoff %v", a, d)
		}
	}
}

func TestBackoffClampsRetryAfter(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", "100000")
	if d := backoff(Policy{Base: time.Second, Cap: 10 * time.Second}, 1, resp); d > maxRetryAfter {
		t.Errorf("Retry-After not clamped: %v > %v", d, maxRetryAfter)
	}
}
