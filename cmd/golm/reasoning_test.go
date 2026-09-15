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

type wire struct {
	Model           string `json:"model"`
	MaxTokens       int    `json:"max_completion_tokens"`
	MaxTokensLegacy int    `json:"max_tokens"`
	ReasoningEffort string `json:"reasoning_effort"`
}

func reasoningServer(t *testing.T, got *wire) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, got); err != nil {
			t.Errorf("decode request: %v (%s)", err, b)
		}
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":11,"completion_tokens":7,
			"completion_tokens_details":{"reasoning_tokens":4},
			"prompt_tokens_details":{"cached_tokens":5}}}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runWith(t *testing.T, srv *httptest.Server, args ...string) (string, int) {
	t.Helper()
	t.Setenv("GOLM_TEST_KEY", "k")
	base := []string{"--provider", "openai", "--base-url", srv.URL + "/v1", "--api-key-env", "GOLM_TEST_KEY", "--no-stream"}
	var out, errOut bytes.Buffer
	code := run(append(base, args...), strings.NewReader(""), &out, &errOut)
	return errOut.String(), code
}

func TestEffortFlagReachesTheWire(t *testing.T) {
	var got wire
	srv := reasoningServer(t, &got)

	if errOut, code := runWith(t, srv, "--model", "gpt-5", "--effort", "high", "hello"); code != 0 {
		t.Fatalf("run = %d: %s", code, errOut)
	}
	if got.ReasoningEffort != "high" {
		t.Errorf("reasoning_effort = %q, want %q — the flag never reached the request", got.ReasoningEffort, "high")
	}
}

func TestEffortIsRefusedBeforeTheCall(t *testing.T) {
	var got wire
	srv := reasoningServer(t, &got)

	errOut, code := runWith(t, srv, "--model", "gpt-5", "--effort", "maximum", "hello")
	if code == 0 {
		t.Fatal("an unknown effort should fail")
	}
	if !strings.Contains(errOut, "invalid --effort") {
		t.Errorf("stderr = %q, want it to name the flag and the valid rungs", errOut)
	}
	if got.Model != "" {
		t.Error("nothing should have been sent")
	}
}

func TestThinkIsValidatedRatherThanSwallowed(t *testing.T) {
	var got wire
	srv := reasoningServer(t, &got)

	errOut, code := runWith(t, srv, "--model", "gpt-5", "--think", "atuo", "hello")
	if code == 0 {
		t.Fatal("a misspelt --think should fail")
	}
	if !strings.Contains(errOut, "invalid --think") {
		t.Errorf("stderr = %q, want the flag named", errOut)
	}

	tc, err := parseThink("disabled", 0)
	if err != nil {
		t.Fatalf("parseThink(disabled): %v", err)
	}
	if tc.Mode != golm.ThinkingDisabled {
		t.Errorf("mode = %v, want ThinkingDisabled", tc.Mode)
	}
	off, _ := parseThink("off", 0)
	if tc.Mode == off.Mode {
		t.Error("disabled and off must not be the same request")
	}
}

func TestThinkBudgetIsSettable(t *testing.T) {
	tc, err := parseThink("budget", 4096)
	if err != nil {
		t.Fatalf("parseThink: %v", err)
	}
	if tc.Budget != 4096 {
		t.Errorf("budget = %d, want the flag's 4096", tc.Budget)
	}

	tc, _ = parseThink("budget", 0)
	if tc.Budget != defaultThinkBudget {
		t.Errorf("budget = %d, want the default", tc.Budget)
	}

	bf := &backendFlags{thinkBudget: 4096}
	if err := bf.override(&golm.Agent{}); err == nil {
		t.Error("--think-budget without --think budget should be refused")
	}
}

func TestMaxTokensFlagReachesTheWire(t *testing.T) {
	var got wire
	srv := reasoningServer(t, &got)
	if errOut, code := runWith(t, srv, "--model", "gpt-4o", "--max-tokens", "321", "hello"); code != 0 {
		t.Fatalf("run = %d: %s", code, errOut)
	}
	if got.MaxTokens != 321 && got.MaxTokensLegacy != 321 {
		t.Errorf("output ceiling on the wire = %d/%d, want 321", got.MaxTokens, got.MaxTokensLegacy)
	}
}

func TestUsageIsReportedFromTheResponse(t *testing.T) {
	var got wire
	srv := reasoningServer(t, &got)
	errOut, code := runWith(t, srv, "--model", "gpt-5", "--usage", "hello")
	if code != 0 {
		t.Fatalf("run = %d: %s", code, errOut)
	}

	for _, want := range []string{"input 11", "thinking 4", "cache read 5"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("usage line %q missing %q", errOut, want)
		}
	}
}
