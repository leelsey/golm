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

func TestStreamDoneWithoutFinishErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"partial"}}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := New("k").WithBaseURL(srv.URL)
	_, err := c.Stream(context.Background(), golm.Request{Model: "m", Messages: []golm.Message{golm.UserText("hi")}}, func(golm.StreamEvent) error { return nil })
	if err == nil {
		t.Fatal("[DONE] without finish_reason returned nil error (silent partial answer)")
	}
}

func TestStreamFinishWithoutDoneSucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"full"}}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`+"\n\n")
	}))
	defer srv.Close()

	c := New("k").WithBaseURL(srv.URL)
	resp, err := c.Stream(context.Background(), golm.Request{Model: "m", Messages: []golm.Message{golm.UserText("hi")}}, func(golm.StreamEvent) error { return nil })
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Message.Text() != "full" {
		t.Errorf("text = %q, want full", resp.Message.Text())
	}
}
