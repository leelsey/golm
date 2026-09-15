// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package clibackend

import (
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

func TestToolResultMediaPlaceholder(t *testing.T) {
	got := messageText(golm.Message{Role: golm.RoleTool, Content: []golm.Content{golm.ToolResult{
		ToolUseID: "t1",
		Name:      "screenshot",
		Content: []golm.ToolContent{
			golm.Text{Text: "captured "},
			golm.Image{MediaType: "image/png", Data: make([]byte, 12043)},
		},
	}}})
	want := "captured [image image/png 12043 bytes]"
	if got != want {
		t.Errorf("messageText = %q, want %q", got, want)
	}
}

func TestToolResultTextUnchanged(t *testing.T) {
	got := messageText(golm.Message{Role: golm.RoleTool, Content: []golm.Content{
		golm.ToolResult{ToolUseID: "t1", Name: "echo", Content: golm.ToolText("echoed")},
	}})
	if got != "echoed" {
		t.Errorf("messageText = %q, want %q", got, "echoed")
	}
}

// A URL-only image carries no bytes.
func TestToolResultURLImageKeepsTheURL(t *testing.T) {
	got := messageText(golm.Message{Role: golm.RoleTool, Content: []golm.Content{golm.ToolResult{
		ToolUseID: "t1",
		Name:      "fetch",
		Content:   []golm.ToolContent{golm.Image{MediaType: "image/png", URL: "https://example.test/a.png"}},
	}}})
	if !strings.Contains(got, "https://example.test/a.png") {
		t.Errorf("rendered %q, want the URL preserved", got)
	}
}
