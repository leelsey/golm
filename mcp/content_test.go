// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/internal/rpc"
)

func TestCallToolSurfacesNonTextContent(t *testing.T) {
	a, b := rpc.NewPipe()
	srv := rpc.NewServer()
	srv.Handle("tools/call", func(context.Context, json.RawMessage) (any, error) {
		return CallToolResult{Content: []Content{
			{Type: "text", Text: "hello "},
			{Type: "image", MimeType: "image/png", Data: "AAAA"},
		}}, nil
	})
	go func() { _ = srv.Serve(context.Background(), a) }()

	c := NewClient(b)
	defer c.Close()
	out, isErr, err := c.CallTool(context.Background(), "x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if isErr {
		t.Error("unexpected isError")
	}
	if out != "hello [image image/png]" {
		t.Errorf("got %q, want %q", out, "hello [image image/png]")
	}
}

func callWire(t *testing.T, tool golm.Tool) string {
	t.Helper()
	srv := NewServer("test-server", "0.1")
	srv.AddTools(tool)
	res, err := srv.handleCallTool(context.Background(), json.RawMessage(`{"name":"`+tool.Name()+`"}`))
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestCallToolImageBlock(t *testing.T) {
	tool := golm.NewContentTool("shot", "returns an image", nil,
		func(context.Context, json.RawMessage) ([]golm.ToolContent, error) {
			return []golm.ToolContent{golm.Image{MediaType: "image/png", Data: []byte{0x89, 'P', 'N', 'G'}}}, nil
		})
	want := `{"content":[{"type":"image","mimeType":"image/png","data":"iVBORw=="}]}`
	if got := callWire(t, tool); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestCallToolImageURLBecomesText(t *testing.T) {
	tool := golm.NewContentTool("linked", "returns an image URL", nil,
		func(context.Context, json.RawMessage) ([]golm.ToolContent, error) {
			return []golm.ToolContent{golm.Image{MediaType: "image/png", URL: "https://example.invalid/a.png"}}, nil
		})
	want := `{"content":[{"type":"text","text":"https://example.invalid/a.png"}]}`
	if got := callWire(t, tool); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestCallToolTextResultUnchanged(t *testing.T) {
	want := `{"content":[{"type":"text","text":"echoed:{}"}]}`
	if got := callWire(t, echoTool()); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
