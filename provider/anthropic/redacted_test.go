// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package anthropic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leelsey/golm"
)

func TestStreamRedactedThinkingNoEmptyTextBlock(t *testing.T) {
	events := []string{
		`event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":1}}}`,
		`event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"redacted_thinking","data":"xxx"}}`,
		`event: content_block_stop
data: {"index":0}`,
		`event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text"}}`,
		`event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"hi"}}`,
		`event: content_block_stop
data: {"index":1}`,
		`event: message_stop
data: {"type":"message_stop"}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		for _, e := range events {
			fmt.Fprint(w, e, "\n\n")
		}
	}))
	defer srv.Close()

	c := New("key").WithBaseURL(srv.URL)
	resp, err := c.Stream(context.Background(), golm.Request{Model: "m", Messages: []golm.Message{golm.UserText("hi")}}, func(golm.StreamEvent) error { return nil })
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(resp.Message.Content) != 1 {
		t.Fatalf("content blocks = %d, want 1 (no empty/redacted block): %+v", len(resp.Message.Content), resp.Message.Content)
	}
	if resp.Message.Text() != "hi" {
		t.Errorf("text = %q, want hi", resp.Message.Text())
	}
}
