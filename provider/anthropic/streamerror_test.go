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

func TestStreamErrorEventSurfaces(t *testing.T) {
	events := []string{
		`event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":2}}}`,
		`event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
		`event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"par"}}`,
		`event: error
data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		for _, e := range events {
			fmt.Fprint(w, e, "\n\n")
		}
	}))
	defer srv.Close()

	c := New("key").WithBaseURL(srv.URL)
	_, err := c.Stream(context.Background(), golm.Request{Model: "m", Messages: []golm.Message{golm.UserText("hi")}}, func(golm.StreamEvent) error { return nil })
	if err == nil {
		t.Fatal("mid-stream error event returned nil error (failure masked as success)")
	}
}
