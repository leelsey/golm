// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

func TestFormatUsage(t *testing.T) {
	got := formatUsage(golm.Usage{InputTokens: 10, OutputTokens: 5})
	if got != "tokens: input 10, output 5, total 15" {
		t.Errorf("formatUsage = %q", got)
	}
	got = formatUsage(golm.Usage{InputTokens: 10, OutputTokens: 5, ThinkingTokens: 3, CacheReadTokens: 4, CacheWriteTokens: 2})
	if got != "tokens: input 10, output 5, thinking 3, cache read 4, cache write 2, total 18" {
		t.Errorf("formatUsage = %q", got)
	}
}

func TestPrintRunUsage(t *testing.T) {
	res := golm.Result{
		StepUsage: []golm.Usage{
			{InputTokens: 10, OutputTokens: 5},
			{InputTokens: 20, OutputTokens: 7},
		},
		Usage: golm.Usage{InputTokens: 30, OutputTokens: 12},
	}
	var sb strings.Builder
	printRunUsage(&sb, res)
	want := "golm: step 1 tokens: input 10, output 5, total 15\n" +
		"golm: step 2 tokens: input 20, output 7, total 27\n" +
		"golm: tokens: input 30, output 12, total 42\n"
	if sb.String() != want {
		t.Errorf("printRunUsage =\n%q\nwant\n%q", sb.String(), want)
	}

	res.StepUsage = res.StepUsage[:1]
	res.Usage = res.StepUsage[0]
	sb.Reset()
	printRunUsage(&sb, res)
	if sb.String() != "golm: tokens: input 10, output 5, total 15\n" {
		t.Errorf("single-step output = %q", sb.String())
	}
}

func mockOpenAI(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Stream bool `json:"stream"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		if req.Stream {
			w.Header().Set("content-type", "text/event-stream")
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"pong\"}}]}\n\n")
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
			io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":4}}\n\n")
			io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":4}}`)
	}))
}

func TestUsageFlagEndToEnd(t *testing.T) {
	srv := mockOpenAI(t)
	defer srv.Close()
	base := []string{"--provider", "openai", "--base-url", srv.URL + "/v1", "--model", "m"}

	for _, mode := range []string{"no-stream", "stream"} {
		args := append([]string{}, base...)
		if mode == "no-stream" {
			args = append(args, "--no-stream")
		}
		var out, errOut bytes.Buffer
		code := run(append(append([]string{"--usage"}, args...), "ping"), strings.NewReader(""), &out, &errOut)
		if code != 0 {
			t.Fatalf("%s: exit = %d, stderr=%q", mode, code, errOut.String())
		}
		if !strings.Contains(out.String(), "pong") {
			t.Errorf("%s: response missing: %q", mode, out.String())
		}
		if !strings.Contains(errOut.String(), "golm: tokens: input 7, output 4, total 11") {
			t.Errorf("%s: usage line missing: %q", mode, errOut.String())
		}
	}

	var out, errOut bytes.Buffer
	if code := run(append(append([]string{}, base...), "ping"), strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if strings.Contains(errOut.String(), "tokens:") {
		t.Errorf("usage printed without --usage: %q", errOut.String())
	}
}

func TestTUIUsageFlagEndToEnd(t *testing.T) {
	srv := mockOpenAI(t)
	defer srv.Close()

	var out, errOut bytes.Buffer
	code := run([]string{"tui", "--usage", "--provider", "openai", "--base-url", srv.URL + "/v1", "--model", "m"},
		strings.NewReader("hello\n/usage\n/exit\n"), &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%q", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "golm: tokens: input 7, output 4, total 11") {
		t.Errorf("--usage per-turn line missing: %q", errOut.String())
	}
	if !strings.Contains(out.String(), "(last run: input 7, output 4, total 11)") {
		t.Errorf("/usage command output missing: %q", out.String())
	}
}
