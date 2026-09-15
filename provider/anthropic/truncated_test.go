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

func TestStreamEOFAfterStopReasonSucceeds(t *testing.T) {
	events := []string{
		`event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":1}}}`,
		`event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
		`event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"full answer"}}`,
		`event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
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
		t.Fatalf("Stream: %v (complete answer discarded)", err)
	}
	if resp.Message.Text() != "full answer" {
		t.Errorf("text = %q, want full answer", resp.Message.Text())
	}
}
