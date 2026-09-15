// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leelsey/golm"
)

func TestStreamToolStartEmittedOnce(t *testing.T) {
	chunks := []string{
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"f"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"f","arguments":"{}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			fl.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	defer srv.Close()

	c := New("k").WithBaseURL(srv.URL)
	starts := 0
	_, err := c.Stream(context.Background(), golm.Request{Model: "gpt-4o", Messages: []golm.Message{golm.UserText("hi")}}, func(ev golm.StreamEvent) error {
		if ev.Type == golm.EventToolStart {
			starts++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if starts != 1 {
		t.Errorf("EventToolStart fired %d times, want 1", starts)
	}
}
