// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package httpwire_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leelsey/golm/httpretry"
	"github.com/leelsey/golm/provider/internal/httpwire"
)

const key = "sk-test-0123456789abcdef"

func post(t *testing.T, h http.HandlerFunc) (int, string, http.Header, error) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	var gotStatus int
	var gotBody string
	var gotHdr http.Header
	_, _, err := httpwire.Post(context.Background(), srv.Client(), httpretry.Policy{},
		httpwire.Request{
			URL:    srv.URL,
			APIKey: key,
			Header: func(hd http.Header) { hd.Set("authorization", "Bearer "+key) },
			Body:   map[string]string{"m": "x"},
		},
		func(status int, body string, hd http.Header) error {
			gotStatus, gotBody, gotHdr = status, body, hd
			return errors.New("upstream")
		})
	return gotStatus, gotBody, gotHdr, err
}

// An endpoint that echoes the credential it was sent must not get it back out through the error.
func TestFailureBodyIsRedacted(t *testing.T) {
	status, body, _, err := post(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key ` + key + ` supplied"}`))
	})
	if err == nil {
		t.Fatal("want an error for 401")
	}
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", status)
	}
	if strings.Contains(body, key) {
		t.Error("the API key survived into the error body")
	}
	if !strings.Contains(body, "bad key") {
		t.Errorf("the server's own words were lost: %q", body)
	}
}

// Headers must outlive the closed body: the Anthropic adapter reads usage off them.
func TestFailureHeadersSurviveTheClosedBody(t *testing.T) {
	_, _, hdr, _ := post(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("anthropic-ratelimit-input-tokens-remaining", "17")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	if got := hdr.Get("anthropic-ratelimit-input-tokens-remaining"); got != "17" {
		t.Errorf("header = %q, want 17", got)
	}
}

func TestSuccessReturnsAnOpenBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("content-type"); got != "application/json" {
			t.Errorf("content-type = %q", got)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	resp, _, err := httpwire.Post(context.Background(), srv.Client(), httpretry.Policy{},
		httpwire.Request{URL: srv.URL, APIKey: key, Body: map[string]string{}},
		func(int, string, http.Header) error { return errors.New("unreachable") })
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
}
