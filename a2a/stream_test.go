// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package a2a

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
)

type plainHandler struct{}

func (plainHandler) HandleMessage(_ context.Context, msg Message) ([]Part, error) {
	return []Part{TextPart("plain:" + msg.Text())}, nil
}

type failHandler struct{}

func (failHandler) HandleMessage(_ context.Context, _ Message) ([]Part, error) {
	return nil, errors.New("boom")
}

func TestStreamFallbackNonStreamingHandler(t *testing.T) {
	hs := httptest.NewServer(NewServer(CardForAgent("p", "p", "http://x/"), plainHandler{}).HTTPHandler())
	defer hs.Close()
	out, err := NewClient(hs.URL).SendMessageStream(context.Background(), "hi", nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if out != "plain:hi" {
		t.Errorf("non-streaming handler over stream = %q, want plain:hi", out)
	}
}

func TestStreamPropagatesHandlerError(t *testing.T) {
	hs := httptest.NewServer(NewServer(CardForAgent("e", "e", "http://x/"), failHandler{}).HTTPHandler())
	defer hs.Close()
	if _, err := NewClient(hs.URL).SendMessageStream(context.Background(), "hi", nil); err == nil {
		t.Fatal("expected error from a failed streaming task, got nil")
	}
}

func TestCancelDoesNotClobberCompletedTask(t *testing.T) {
	hs := httptest.NewServer(NewServer(CardForAgent("p", "p", "http://x/"), plainHandler{}).HTTPHandler())
	defer hs.Close()
	c := NewClient(hs.URL)
	var sent Task
	if err := c.call(context.Background(), "message/send", MessageSendParams{Message: userMessage("hi")}, &sent); err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := c.call(context.Background(), "tasks/cancel", TaskQueryParams{ID: sent.ID}, nil); err == nil {
		t.Fatal("cancel of a completed task should be rejected")
	}
	var got Task
	if err := c.call(context.Background(), "tasks/get", TaskQueryParams{ID: sent.ID}, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.State != StateCompleted {
		t.Errorf("cancel clobbered terminal task: state = %q, want completed", got.Status.State)
	}
}
