// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package anthropic

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leelsey/golm"
)

func TestCompleteCacheTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"id":"msg","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":5,"cache_read_input_tokens":100,"cache_creation_input_tokens":40}}`)
	}))
	defer srv.Close()

	c := New("key").WithBaseURL(srv.URL)
	resp, err := c.Complete(context.Background(), golm.Request{Model: "m", Messages: []golm.Message{golm.UserText("hello")}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	want := golm.Usage{InputTokens: 143, OutputTokens: 5, CacheReadTokens: 100, CacheWriteTokens: 40}
	if resp.Usage != want {
		t.Errorf("usage = %+v, want %+v", resp.Usage, want)
	}
}

func TestStreamCacheTokens(t *testing.T) {
	events := []string{
		`event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":2,"cache_read_input_tokens":50,"cache_creation_input_tokens":10}}}`,
		`event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
		`event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`,
		`event: content_block_stop
data: {"index":0}`,
		`event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`,
		`event: message_stop
data: {"type":"message_stop"}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		for _, e := range events {
			fmt.Fprint(w, e, "\n\n")
		}
	}))
	defer srv.Close()

	c := New("key").WithBaseURL(srv.URL)
	resp, err := c.Stream(context.Background(), golm.Request{Model: "m", Messages: []golm.Message{golm.UserText("hello")}}, func(golm.StreamEvent) error { return nil })
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	want := golm.Usage{InputTokens: 62, OutputTokens: 7, CacheReadTokens: 50, CacheWriteTokens: 10}
	if resp.Usage != want {
		t.Errorf("usage = %+v, want %+v", resp.Usage, want)
	}
}

// Anthropic bills thinking at the output rate and reports it inside output_tokens.
func TestThinkingStaysInsideOutputTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"id":"msg","content":[{"type":"thinking","thinking":"..."},{"type":"text","text":"hi"}],`+
			`"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":500}}`)
	}))
	defer srv.Close()

	c := New("k").WithBaseURL(srv.URL)
	resp, err := c.Complete(context.Background(), golm.Request{
		Model:    "m",
		Messages: []golm.Message{golm.UserText("hi")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage.OutputTokens != 500 {
		t.Errorf("OutputTokens=%d want 500", resp.Usage.OutputTokens)
	}
	if resp.Usage.ThinkingTokens != 0 {
		t.Errorf("ThinkingTokens=%d want 0 — Anthropic reports thinking inside output_tokens, "+
			"so a consumer adding the two would bill this turn twice",
			resp.Usage.ThinkingTokens)
	}
}

// The cache figures are a breakdown of InputTokens, never additional to it.
func TestCacheTokensStayWithinInput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"id":"msg","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn",`+
			`"usage":{"input_tokens":7,"output_tokens":1,"cache_read_input_tokens":900,"cache_creation_input_tokens":93}}`)
	}))
	defer srv.Close()

	c := New("k").WithBaseURL(srv.URL)
	resp, err := c.Complete(context.Background(), golm.Request{
		Model:    "m",
		Messages: []golm.Message{golm.UserText("hi")},
	})
	if err != nil {
		t.Fatal(err)
	}
	u := resp.Usage
	if got := u.CacheReadTokens + u.CacheWriteTokens; got > u.InputTokens {
		t.Fatalf("cache %d exceeds input %d — the breakdown must sum into the total", got, u.InputTokens)
	}

	if fresh := u.InputTokens - u.CacheReadTokens - u.CacheWriteTokens; fresh != 7 {
		t.Errorf("uncached remainder=%d want 7", fresh)
	}
}

// A proxy that knows what a FAILED call cost must be able to say so.
func TestUsageFromHeaderReachesTheErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(UsageHeader,
			`{"prompt_tokens":42101,"completion_tokens":95,"cached_input_tokens":33096,"cache_creation_tokens":9000}`)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"type":"error","error":{"message":"upstream died"}}`))
	}))
	defer srv.Close()

	c := New("k").WithBaseURL(srv.URL).WithoutRetry()
	resp, err := c.Complete(context.Background(), golm.Request{
		Model: "m", Messages: []golm.Message{golm.UserText("hi")},
	})
	if err == nil {
		t.Fatal("a 502 must still surface as an error")
	}
	if resp.Usage.InputTokens != 42101 {
		t.Errorf("input=%d want 42101 — the failure's cost was discarded", resp.Usage.InputTokens)
	}
	if resp.Usage.CacheReadTokens != 33096 || resp.Usage.CacheWriteTokens != 9000 {
		t.Errorf("cache read=%d write=%d want 33096/9000 — the split was lost",
			resp.Usage.CacheReadTokens, resp.Usage.CacheWriteTokens)
	}
	if resp.Usage.OutputTokens != 95 {
		t.Errorf("output=%d want 95", resp.Usage.OutputTokens)
	}
}

// No header means UNKNOWN, and unknown must not be dressed up as zero-cost.
func TestNoUsageHeaderLeavesTheResponseEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"type":"error"}`))
	}))
	defer srv.Close()

	c := New("k").WithBaseURL(srv.URL).WithoutRetry()
	resp, err := c.Complete(context.Background(), golm.Request{
		Model: "m", Messages: []golm.Message{golm.UserText("hi")},
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if resp.Usage.InputTokens != 0 || resp.Usage.OutputTokens != 0 {
		t.Errorf("usage must stay empty when nothing reported it, got %+v", resp.Usage)
	}
}
