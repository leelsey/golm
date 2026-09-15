// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leelsey/golm"
)

func TestComplete(t *testing.T) {
	var body struct {
		Model    string `json:"model"`
		Messages []struct {
			Role       string `json:"role"`
			Content    any    `json:"content"`
			ToolCallID string `json:"tool_call_id"`
		} `json:"messages"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("authorization") != "Bearer key" {
			t.Errorf("bad authorization header: %q", r.Header.Get("authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"hi","tool_calls":[{"id":"t1","type":"function","function":{"name":"echo","arguments":"{\"x\":1}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":5}}`)
	}))
	defer srv.Close()

	c := New("key").WithBaseURL(srv.URL)
	resp, err := c.Complete(context.Background(), golm.Request{
		Model:  "m",
		System: golm.SystemPrompt{{Text: "sys"}},
		Messages: []golm.Message{
			golm.UserText("hello"),
			{Role: golm.RoleTool, Content: []golm.Content{golm.ToolResult{ToolUseID: "t1", Content: golm.ToolText("echoed")}}},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.StopReason != golm.StopToolUse || resp.Message.Text() != "hi" {
		t.Errorf("resp = %+v", resp)
	}
	uses := resp.Message.ToolUses()
	if len(uses) != 1 || uses[0].ID != "t1" || string(uses[0].Input) != `{"x":1}` {
		t.Errorf("tool uses = %+v", uses)
	}
	if body.Messages[0].Role != "system" {
		t.Errorf("system not prepended: %+v", body.Messages[0])
	}
	last := body.Messages[len(body.Messages)-1]
	if last.Role != "tool" || last.ToolCallID != "t1" {
		t.Errorf("tool result not mapped: %+v", last)
	}
}

func TestStream(t *testing.T) {
	chunks := []string{
		`{"choices":[{"delta":{"content":"Hel"}}]}`,
		`{"choices":[{"delta":{"content":"lo"}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"t1","type":"function","function":{"name":"echo","arguments":"{\"x\""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":":1}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":9}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		for _, ch := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", ch)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := New("key").WithBaseURL(srv.URL)
	var text string
	resp, err := c.Stream(context.Background(), golm.Request{Model: "m", Messages: []golm.Message{golm.UserText("hi")}}, func(ev golm.StreamEvent) error {
		if ev.Type == golm.EventTextDelta {
			text += ev.Text
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if text != "Hello" {
		t.Errorf("text = %q", text)
	}
	uses := resp.Message.ToolUses()
	if len(uses) != 1 || uses[0].Name != "echo" || string(uses[0].Input) != `{"x":1}` {
		t.Errorf("assembled tool use = %+v", uses)
	}
	if resp.StopReason != golm.StopToolUse {
		t.Errorf("stop = %q", resp.StopReason)
	}
	if resp.Usage.OutputTokens != 9 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}
