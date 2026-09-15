// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package anthropic

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

// Anthropic sends the whole input and cache accounting in the very first frame.
func TestStreamReturnsUsageItAlreadyReceivedWhenItFails(t *testing.T) {
	events := []string{
		`event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":11,"cache_read_input_tokens":4000,"cache_creation_input_tokens":250}}}`,
		`event: error
data: {"type":"error","error":{"type":"overloaded_error","message":"upstream is busy"}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		for _, e := range events {
			fmt.Fprint(w, e, "\n\n")
		}
	}))
	defer srv.Close()

	c := New("key").WithBaseURL(srv.URL)
	resp, err := c.Stream(context.Background(), golm.Request{
		Model: "m", Messages: []golm.Message{golm.UserText("hello")},
	}, func(golm.StreamEvent) error { return nil })

	if err == nil {
		t.Fatal("expected the overloaded_error frame to surface")
	}
	u := resp.Usage
	if u.InputTokens != 4261 {
		t.Errorf("InputTokens=%d want 4261 (11 fresh + 4000 read + 250 write)", u.InputTokens)
	}
	if u.CacheReadTokens != 4000 || u.CacheWriteTokens != 250 {
		t.Errorf("cache read=%d write=%d want 4000/250", u.CacheReadTokens, u.CacheWriteTokens)
	}
}

// message_delta restates the final totals.
func TestStreamTakesTheRestatedTotalsFromMessageDelta(t *testing.T) {
	events := []string{
		`event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`,
		`event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":12,"output_tokens":90,"cache_read_input_tokens":8000,"cache_creation_input_tokens":300}}`,
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
	resp, err := c.Stream(context.Background(), golm.Request{
		Model: "m", Messages: []golm.Message{golm.UserText("hello")},
	}, func(golm.StreamEvent) error { return nil })
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	u := resp.Usage
	if u.CacheReadTokens != 8000 || u.CacheWriteTokens != 300 {
		t.Errorf("cache read=%d write=%d want 8000/300 from the restated totals", u.CacheReadTokens, u.CacheWriteTokens)
	}
	if u.InputTokens != 8312 {
		t.Errorf("InputTokens=%d want 8312", u.InputTokens)
	}
	if u.OutputTokens != 90 {
		t.Errorf("OutputTokens=%d want 90", u.OutputTokens)
	}
}

// A delta that omits the input figures must not be read as a report of zero.
func TestStreamKeepsOpeningTotalsWhenTheDeltaOmitsThem(t *testing.T) {
	events := []string{
		`event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":7,"cache_read_input_tokens":900,"cache_creation_input_tokens":100}}}`,
		`event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":25}}`,
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
	resp, err := c.Stream(context.Background(), golm.Request{
		Model: "m", Messages: []golm.Message{golm.UserText("hello")},
	}, func(golm.StreamEvent) error { return nil })
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Usage.CacheReadTokens != 900 || resp.Usage.InputTokens != 1007 {
		t.Errorf("usage=%+v — the opening frame's totals were erased by an absent field", resp.Usage)
	}
}

// A malformed message_start was the one decode failure the stream swallowed.
func TestStreamRefusesToSucceedWithAnUnreadableMessageStart(t *testing.T) {
	events := []string{
		`event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":"not-a-number"}}}`,
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
	_, err := c.Stream(context.Background(), golm.Request{
		Model: "m", Messages: []golm.Message{golm.UserText("hello")},
	}, func(golm.StreamEvent) error { return nil })
	if err == nil {
		t.Fatal("stream reported success with no input accounting at all")
	}
	if !strings.Contains(err.Error(), "message_start") {
		t.Errorf("err=%v, want it to name the frame that could not be read", err)
	}
}

// encoding/json fills as it walks.
func TestCompleteReturnsUsageDecodedBeforeTheTypeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"id":"msg","content":[],"stop_reason":42,"usage":{"input_tokens":300,"output_tokens":20,"cache_read_input_tokens":250}}`)
	}))
	defer srv.Close()

	c := New("key").WithBaseURL(srv.URL)
	resp, err := c.Complete(context.Background(), golm.Request{
		Model: "m", Messages: []golm.Message{golm.UserText("hello")},
	})
	if err == nil {
		t.Fatal("expected the type mismatch to surface")
	}
	if resp.Usage.OutputTokens != 20 || resp.Usage.CacheReadTokens != 250 {
		t.Errorf("usage=%+v — figures decoded before the error were discarded", resp.Usage)
	}
}

// The delta that breaks a grouped assignment.
func TestStreamKeepsCacheWhenTheDeltaRestatesOnlyInput(t *testing.T) {
	events := []string{
		`event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":4,"cache_read_input_tokens":18000,"cache_creation_input_tokens":0}}}`,
		`event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":91,"input_tokens":4}}`,
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
	resp, err := c.Stream(context.Background(), golm.Request{
		Model: "m", Messages: []golm.Message{golm.UserText("hello")},
	}, func(golm.StreamEvent) error { return nil })
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	u := resp.Usage
	if u.CacheReadTokens != 18000 {
		t.Errorf("CacheReadTokens=%d want 18000 — the delta's partial restatement wiped it", u.CacheReadTokens)
	}
	if u.InputTokens != 18004 {
		t.Errorf("InputTokens=%d want 18004", u.InputTokens)
	}
	if u.OutputTokens != 91 {
		t.Errorf("OutputTokens=%d want 91", u.OutputTokens)
	}
}

// A restatement of zero is a report, not an absence.
func TestStreamAcceptsAnExplicitZeroFromTheDelta(t *testing.T) {
	events := []string{
		`event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":4,"cache_read_input_tokens":18000}}}`,
		`event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":10,"input_tokens":50,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}`,
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
	resp, err := c.Stream(context.Background(), golm.Request{
		Model: "m", Messages: []golm.Message{golm.UserText("hello")},
	}, func(golm.StreamEvent) error { return nil })
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Usage.CacheReadTokens != 0 || resp.Usage.InputTokens != 50 {
		t.Errorf("usage=%+v — an explicit zero was read as an absent field", resp.Usage)
	}
}

// A gateway that round-trips the stream through a float-only number type sends 1234.0.
func TestStreamAcceptsFloatEncodedCounts(t *testing.T) {
	events := []string{
		`event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":12.0,"cache_read_input_tokens":9100.0}}}`,
		`event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":47.0}}`,
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
	resp, err := c.Stream(context.Background(), golm.Request{
		Model: "m", Messages: []golm.Message{golm.UserText("hello")},
	}, func(golm.StreamEvent) error { return nil })
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Usage.InputTokens != 9112 || resp.Usage.CacheReadTokens != 9100 || resp.Usage.OutputTokens != 47 {
		t.Errorf("usage=%+v — float-encoded counts were not read", resp.Usage)
	}
}
