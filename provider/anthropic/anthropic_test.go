// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package anthropic

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
		System   string `json:"system"`
		Messages []struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "key" {
			t.Errorf("missing x-api-key header")
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"id":"msg","content":[{"type":"text","text":"hi"},{"type":"tool_use","id":"t1","name":"echo","input":{"x":1}}],"stop_reason":"tool_use","usage":{"input_tokens":3,"output_tokens":5}}`)
	}))
	defer srv.Close()

	c := New("key").WithBaseURL(srv.URL)
	resp, err := c.Complete(context.Background(), golm.Request{
		Model:  "m",
		System: golm.SystemPrompt{{Text: "sys"}},
		Messages: []golm.Message{
			golm.UserText("hello"),
			{Role: golm.RoleAssistant, Content: []golm.Content{golm.ToolUse{ID: "t1", Name: "echo", Input: json.RawMessage(`{"x":1}`)}}},
			{Role: golm.RoleTool, Content: []golm.Content{golm.ToolResult{ToolUseID: "t1", Content: golm.ToolText("echoed")}}},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.StopReason != golm.StopToolUse {
		t.Errorf("stop reason = %q", resp.StopReason)
	}
	if resp.Message.Text() != "hi" {
		t.Errorf("text = %q", resp.Message.Text())
	}
	uses := resp.Message.ToolUses()
	if len(uses) != 1 || uses[0].Name != "echo" {
		t.Errorf("tool uses = %+v", uses)
	}
	if resp.Usage.InputTokens != 3 || resp.Usage.OutputTokens != 5 {
		t.Errorf("usage = %+v", resp.Usage)
	}

	if body.Model != "m" || body.System != "sys" {
		t.Errorf("model/system not mapped: %+v", body)
	}
	if len(body.Messages) != 3 {
		t.Fatalf("messages len = %d", len(body.Messages))
	}
	if body.Messages[2].Role != "user" || body.Messages[2].Content[0]["type"] != "tool_result" {
		t.Errorf("tool role not mapped to user/tool_result: %+v", body.Messages[2])
	}
}

func TestStream(t *testing.T) {
	events := []string{
		`event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":2}}}`,
		`event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
		`event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`,
		`event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`,
		`event: content_block_stop
data: {"index":0}`,
		`event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"t1","name":"echo"}}`,
		`event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"x\":1}"}}`,
		`event: content_block_stop
data: {"index":1}`,
		`event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`,
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
		t.Errorf("streamed text = %q, want %q", text, "Hello")
	}
	if resp.StopReason != golm.StopToolUse {
		t.Errorf("stop reason = %q", resp.StopReason)
	}
	uses := resp.Message.ToolUses()
	if len(uses) != 1 || uses[0].Name != "echo" || string(uses[0].Input) != `{"x":1}` {
		t.Errorf("assembled tool use wrong: %+v", uses)
	}
	if resp.Message.Text() != "Hello" {
		t.Errorf("assembled text = %q", resp.Message.Text())
	}
}
