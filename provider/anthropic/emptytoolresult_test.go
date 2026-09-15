// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leelsey/golm"
)

func TestEmptyToolResultGetsPlaceholder(t *testing.T) {
	var body struct {
		Messages []struct {
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"id":"m","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer srv.Close()

	c := New("key").WithBaseURL(srv.URL)
	_, err := c.Complete(context.Background(), golm.Request{
		Model: "m",
		Messages: []golm.Message{
			{Role: golm.RoleTool, Content: []golm.Content{golm.ToolResult{ToolUseID: "t1"}}},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(body.Messages) != 1 {
		t.Fatalf("messages = %d", len(body.Messages))
	}
	content, _ := body.Messages[0].Content[0]["content"].(string)
	if content == "" {
		t.Errorf("empty tool_result content sent as empty string; want placeholder")
	}
}
