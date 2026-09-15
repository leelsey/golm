// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

func TestReasoningEffortFromBudget(t *testing.T) {
	body := captureBody(t, golm.Request{
		Model:    "o3-mini",
		Messages: []golm.Message{golm.UserText("hi")},
		Thinking: golm.ThinkingConfig{Mode: golm.ThinkingBudget, Budget: 1024},
	})
	if !strings.Contains(body, `"reasoning_effort":"low"`) {
		t.Errorf("budget 1024 should map to low effort, got: %s", body)
	}
}

func TestToolResultErrorMarked(t *testing.T) {
	body := captureBody(t, golm.Request{
		Model: "gpt-4o",
		Messages: []golm.Message{
			{Role: golm.RoleTool, Content: []golm.Content{golm.ToolResult{ToolUseID: "t1", Content: golm.ToolText("boom"), IsError: true}}},
		},
	})
	if !strings.Contains(body, "Error: boom") {
		t.Errorf("tool error not marked on the wire: %s", body)
	}
}

func TestContentlessAssistantGetsContentField(t *testing.T) {
	body := captureBody(t, golm.Request{
		Model: "gpt-4o",
		Messages: []golm.Message{
			{Role: golm.RoleAssistant, Content: []golm.Content{golm.Thinking{Text: "hmm"}}},
		},
	})
	if !strings.Contains(body, `"content":""`) {
		t.Errorf("content-less assistant message missing content field: %s", body)
	}
}

func TestStreamToolCallMissingIDSkipped(t *testing.T) {
	chunks := []string{
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]}}]}`,
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
	resp, err := c.Stream(context.Background(), golm.Request{Model: "gpt-4o", Messages: []golm.Message{golm.UserText("hi")}}, func(golm.StreamEvent) error { return nil })
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if n := len(resp.Message.ToolUses()); n != 0 {
		t.Errorf("assembled %d tool uses from an id-less tool call, want 0", n)
	}
}
