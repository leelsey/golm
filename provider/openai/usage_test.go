// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leelsey/golm"
)

func TestCompleteUsageDetails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":30,"completion_tokens":25,"prompt_tokens_details":{"cached_tokens":12},"completion_tokens_details":{"reasoning_tokens":20}}}`)
	}))
	defer srv.Close()

	c := New("key").WithBaseURL(srv.URL)
	resp, err := c.Complete(context.Background(), golm.Request{Model: "m", Messages: []golm.Message{golm.UserText("hello")}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	want := golm.Usage{InputTokens: 30, OutputTokens: 5, ThinkingTokens: 20, CacheReadTokens: 12}
	if resp.Usage != want {
		t.Errorf("usage = %+v, want %+v", resp.Usage, want)
	}
}

func TestUsageDetailsClamped(t *testing.T) {
	u := apiUsage{CompletionTokens: 5}
	u.CompletionTokensDetails.ReasoningTokens = 9
	got := u.toUsage()
	if got.OutputTokens != 0 || got.ThinkingTokens != 9 {
		t.Errorf("clamped usage = %+v", got)
	}
}

// Merging must not invent output tokens.
func TestMergeAPIUsage_DoesNotInventOutputTokens(t *testing.T) {
	a := apiUsage{PromptTokens: 100, CompletionTokens: 150}
	b := apiUsage{PromptTokens: 100, CompletionTokens: 150}
	b.CompletionTokensDetails.ReasoningTokens = 50

	got := mergeAPIUsage(a, b).toUsage()
	if got.OutputTokens+got.ThinkingTokens != 150 {
		t.Errorf("output+thinking = %d+%d = %d, want 150 — tokens were invented",
			got.OutputTokens, got.ThinkingTokens, got.OutputTokens+got.ThinkingTokens)
	}
	if got.ThinkingTokens != 50 {
		t.Errorf("thinking=%d want 50", got.ThinkingTokens)
	}
}

// A frame that omits a field must not erase one that was reported.
func TestMergeAPIUsage_OmittedFieldDoesNotClobber(t *testing.T) {
	full := apiUsage{PromptTokens: 4000, CompletionTokens: 10}
	full.PromptTokensDetails.CachedTokens = 3900
	full.PromptTokensDetails.CacheWriteTokens = 50
	partial := apiUsage{CompletionTokens: 120}

	got := mergeAPIUsage(full, partial).toUsage()
	if got.InputTokens != 4000 || got.CacheReadTokens != 3900 || got.CacheWriteTokens != 50 {
		t.Errorf("in=%d cr=%d cw=%d — a partial frame erased the prompt breakdown",
			got.InputTokens, got.CacheReadTokens, got.CacheWriteTokens)
	}
	if got.OutputTokens != 120 {
		t.Errorf("output=%d want 120 — the later, larger count must win", got.OutputTokens)
	}
}
