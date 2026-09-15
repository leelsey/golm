// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/provider/anthropic"
	"github.com/leelsey/golm/provider/google"
	"github.com/leelsey/golm/provider/openai"
)

// An API key must never reach an error, a log line or a prompt.
func TestAPIKeysNeverAppearInErrors(t *testing.T) {
	const key = "sk-SUPERSECRET-DO-NOT-LEAK-0123456789"

	servers := map[string]http.HandlerFunc{
		"401": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"bad key ` + r.Header.Get("x-api-key") +
				r.Header.Get("Authorization") + `"}}`))
		},
		"malformed": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("not json at all"))
		},
		"empty": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		},
	}

	for shape, handler := range servers {
		ts := httptest.NewServer(handler)
		defer ts.Close()
		providers := map[string]golm.Provider{
			"anthropic": anthropic.New(key).WithBaseURL(ts.URL).WithoutRetry(),
			"openai":    openai.New(key).WithBaseURL(ts.URL).WithoutRetry(),
			"google":    google.New(key).WithBaseURL(ts.URL).WithoutRetry(),
		}
		for name, p := range providers {
			t.Run(shape+"/"+name, func(t *testing.T) {
				req := golm.Request{Model: "m", Messages: []golm.Message{golm.UserText("hi")}}
				_, err := p.Complete(context.Background(), req)
				if err == nil {
					return
				}
				if strings.Contains(err.Error(), key) {
					t.Errorf("the API key is in the error: %v", err)
				}

				_, err = p.Stream(context.Background(), req, func(golm.StreamEvent) error { return nil })
				if err != nil && strings.Contains(err.Error(), key) {
					t.Errorf("the API key is in the stream error: %v", err)
				}
			})
		}
	}
}

// A key must not reach the model either.
func TestAPIKeyIsNotInTheRequestBody(t *testing.T) {
	const key = "sk-SUPERSECRET-DO-NOT-LEAK-0123456789"
	var bodies []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 1<<16)
		n, _ := r.Body.Read(buf)
		bodies = append(bodies, string(buf[:n]))
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	for _, p := range []golm.Provider{
		anthropic.New(key).WithBaseURL(ts.URL).WithoutRetry(),
		openai.New(key).WithBaseURL(ts.URL).WithoutRetry(),
		google.New(key).WithBaseURL(ts.URL).WithoutRetry(),
	} {
		_, _ = p.Complete(context.Background(), golm.Request{
			Model: "m", Messages: []golm.Message{golm.UserText("hi")},
		})
	}
	for i, body := range bodies {
		if strings.Contains(body, key) {
			t.Errorf("request %d carries the key in its BODY, not just its header: %s", i, body)
		}
	}
}

// Redact is the only place the decision "does a secret reach an error" is ours.
func TestRedact(t *testing.T) {
	const key = "sk-SUPERSECRET-0123456789"
	cases := []struct {
		name, text string
		secrets    []string
		want       string
	}{
		{"absent", "an ordinary error", []string{key}, "an ordinary error"},
		{"present", "bad key " + key, []string{key}, "bad key " + golm.RedactedMarker},
		{"twice", key + " and " + key, []string{key},
			golm.RedactedMarker + " and " + golm.RedactedMarker},
		{"several secrets", "a AAAAAAAAAAAAAA b BBBBBBBBBBBBBB",
			[]string{"AAAAAAAAAAAAAA", "BBBBBBBBBBBBBB"},
			"a " + golm.RedactedMarker + " b " + golm.RedactedMarker},

		{"too short to scrub", "the status is a", []string{"a"}, "the status is a"},
		{"empty secret", "unchanged", []string{""}, "unchanged"},
		{"empty text", "", []string{key}, ""},
		{"no secrets", "unchanged", nil, "unchanged"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := golm.Redact(c.text, c.secrets...); got != c.want {
				t.Errorf("Redact(%q) = %q, want %q", c.text, got, c.want)
			}
		})
	}
}
