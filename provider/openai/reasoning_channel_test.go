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

func serve(t *testing.T, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return New("key").WithBaseURL(srv.URL)
}

func thinkingOf(m golm.Message) string {
	for _, c := range m.Content {
		if th, ok := c.(golm.Thinking); ok {
			return th.Text
		}
	}
	return ""
}

// An OpenAI-compatible server running an open-weight reasoning model may return the deliberation in its own field and leave content empty.
func TestReasoningChannelBecomesAThinkingBlock(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"reasoning_content", `{"choices":[{"message":{"content":"","reasoning_content":"weighed it up"},"finish_reason":"stop"}],"usage":{}}`},
		{"reasoning", `{"choices":[{"message":{"content":"","reasoning":"weighed it up"},"finish_reason":"stop"}],"usage":{}}`},
		{"thinking", `{"choices":[{"message":{"content":"","thinking":"weighed it up"},"finish_reason":"stop"}],"usage":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := serve(t, tc.body).Complete(context.Background(), golm.Request{
				Model: "gpt-oss", Messages: []golm.Message{golm.UserText("hi")},
			})
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}
			if got := thinkingOf(resp.Message); got != "weighed it up" {
				t.Errorf("thinking=%q, want the reasoning channel carried through", got)
			}
		})
	}
}

// Message.Text() is documented as the concatenation of Text blocks.
func TestReasoningDoesNotLeakIntoText(t *testing.T) {
	resp, err := serve(t, `{"choices":[{"message":{"content":"the answer","reasoning_content":"scratch"},"finish_reason":"stop"}],"usage":{}}`).
		Complete(context.Background(), golm.Request{Model: "gpt-oss", Messages: []golm.Message{golm.UserText("hi")}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Message.Text() != "the answer" {
		t.Errorf("Text()=%q, want only the content field", resp.Message.Text())
	}
}

// Streaming is the attack loop's default transport.
func TestReasoningChannelOverStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"weighed \"}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"it up\"},\"finish_reason\":\"stop\"}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	var deltas string
	resp, err := New("key").WithBaseURL(srv.URL).Stream(context.Background(),
		golm.Request{Model: "gpt-oss", Messages: []golm.Message{golm.UserText("hi")}},
		func(ev golm.StreamEvent) error {
			if ev.Type == golm.EventThinkingDelta {
				deltas += ev.Text
			}
			return nil
		})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got := thinkingOf(resp.Message); got != "weighed it up" {
		t.Errorf("thinking=%q, want both deltas assembled", got)
	}
	if deltas != "weighed it up" {
		t.Errorf("thinking deltas=%q, want them emitted as they arrive", deltas)
	}
}

// A refusal arrives in its own field beside finish_reason "stop".
func TestMessageRefusalBecomesStopRefusal(t *testing.T) {
	resp, err := serve(t, `{"choices":[{"message":{"content":"","refusal":"I can't help with that."},"finish_reason":"stop"}],"usage":{}}`).
		Complete(context.Background(), golm.Request{Model: "gpt-oss", Messages: []golm.Message{golm.UserText("hi")}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.StopReason != golm.StopRefusal {
		t.Errorf("StopReason=%q, want %q", resp.StopReason, golm.StopRefusal)
	}
	if resp.Message.Text() != "I can't help with that." {
		t.Errorf("Text()=%q, want the refusal itself so a caller can see why", resp.Message.Text())
	}
}

func TestMessageRefusalOverStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"refusal\":\"I can't help with that.\"},\"finish_reason\":\"stop\"}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	resp, err := New("key").WithBaseURL(srv.URL).Stream(context.Background(),
		golm.Request{Model: "gpt-oss", Messages: []golm.Message{golm.UserText("hi")}},
		func(golm.StreamEvent) error { return nil })
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.StopReason != golm.StopRefusal {
		t.Errorf("StopReason=%q, want %q", resp.StopReason, golm.StopRefusal)
	}
}
