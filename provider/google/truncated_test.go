// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package google

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

func TestStreamPromptBlockedSurfacesBlockReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		fmt.Fprint(w, `data: {"promptFeedback":{"blockReason":"SAFETY"}}`+"\n\n")
	}))
	defer srv.Close()

	c := New("k").WithBaseURL(srv.URL)
	_, err := c.Stream(context.Background(), golm.Request{Model: "m", Messages: []golm.Message{golm.UserText("hi")}}, func(golm.StreamEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "SAFETY") {
		t.Fatalf("err = %v, want a prompt-blocked error naming the reason", err)
	}
}

func TestStreamWithFinishSucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		fmt.Fprint(w, `data: {"candidates":[{"content":{"parts":[{"text":"full"}]},"finishReason":"STOP"}]}`+"\n\n")
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
