// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

func toolResultBlock(t *testing.T, res golm.ToolResult) map[string]any {
	t.Helper()
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
		Model:    "m",
		Messages: []golm.Message{{Role: golm.RoleTool, Content: []golm.Content{res}}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(body.Messages) != 1 || len(body.Messages[0].Content) != 1 {
		t.Fatalf("messages = %+v", body.Messages)
	}
	return body.Messages[0].Content[0]
}

func TestToolResultImageWireFormat(t *testing.T) {
	b := toolResultBlock(t, golm.ToolResult{ToolUseID: "t1", Name: "shot", Content: []golm.ToolContent{
		golm.Text{Text: "here"},
		golm.Image{MediaType: "image/png", Data: []byte{1, 2, 3}},
	}})
	blocks, ok := b["content"].([]any)
	if !ok {
		t.Fatalf("content = %T (%v); want a block array", b["content"], b["content"])
	}
	if len(blocks) != 2 {
		t.Fatalf("blocks = %+v", blocks)
	}
	text, _ := blocks[0].(map[string]any)
	if text["type"] != "text" || text["text"] != "here" {
		t.Errorf("text block = %+v", text)
	}
	img, _ := blocks[1].(map[string]any)
	src, _ := img["source"].(map[string]any)
	if img["type"] != "image" || src["type"] != "base64" || src["media_type"] != "image/png" {
		t.Errorf("image block = %+v", img)
	}
	if src["data"] != "AQID" {
		t.Errorf("image data = %v; want base64", src["data"])
	}
}

func TestToolResultTextStaysAString(t *testing.T) {
	b := toolResultBlock(t, golm.ToolResult{ToolUseID: "t1", Content: []golm.ToolContent{
		golm.Text{Text: "one "}, golm.Text{Text: "two"},
	}})
	if s, _ := b["content"].(string); s != "one two" {
		t.Errorf("content = %T %v; want the string %q", b["content"], b["content"], "one two")
	}
}

func TestToolResultOfEmptyBlocksGetsPlaceholder(t *testing.T) {
	b := toolResultBlock(t, golm.ToolResult{ToolUseID: "t1", Content: []golm.ToolContent{golm.Text{Text: ""}}})
	if s, _ := b["content"].(string); s != "(no output)" {
		t.Errorf("content = %v; want the placeholder", b["content"])
	}
}

func TestAudioInToolResultRejected(t *testing.T) {
	c := New("k").WithBaseURL("http://127.0.0.1:1")
	req := golm.Request{Model: "m", Messages: []golm.Message{
		{Role: golm.RoleTool, Content: []golm.Content{golm.ToolResult{ToolUseID: "t1", Name: "listen", Content: []golm.ToolContent{
			golm.Audio{MediaType: "audio/wav", Data: []byte{1}},
		}}}},
	}}
	if _, err := c.Complete(context.Background(), req); err == nil || !strings.Contains(err.Error(), "audio") {
		t.Fatalf("Complete err = %v, want an audio-not-supported error", err)
	}
	_, err := c.Stream(context.Background(), req, func(golm.StreamEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "audio") {
		t.Fatalf("Stream err = %v, want an audio-not-supported error", err)
	}
}

func TestToolResultImagesCapability(t *testing.T) {
	if !New("k").Capabilities().ToolResultImages {
		t.Error("ToolResultImages = false; Anthropic carries images in a tool result")
	}
}
