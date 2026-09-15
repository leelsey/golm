// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBaseURLFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"pong-42"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	var out bytes.Buffer
	code := run([]string{
		"--provider", "openai",
		"--base-url", srv.URL + "/v1",
		"--model", "local-model",
		"--no-stream",
		"ping",
	}, strings.NewReader(""), &out, io.Discard)

	if code != 0 {
		t.Fatalf("exit = %d, out=%q", code, out.String())
	}
	if !strings.Contains(out.String(), "pong-42") {
		t.Errorf("base-url backend response missing: %q", out.String())
	}
}

func TestAPIKeyEnvFlag(t *testing.T) {
	t.Setenv("MY_LOCAL_KEY", "secret-123")
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	var out bytes.Buffer
	code := run([]string{
		"--provider", "openai",
		"--base-url", srv.URL + "/v1",
		"--api-key-env", "MY_LOCAL_KEY",
		"--model", "local-model",
		"--no-stream",
		"hi",
	}, strings.NewReader(""), &out, io.Discard)

	if code != 0 {
		t.Fatalf("exit = %d, out=%q", code, out.String())
	}
	if gotAuth != "Bearer secret-123" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer secret-123")
	}
}
