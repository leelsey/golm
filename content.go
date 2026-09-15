// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"encoding/json"
	"strings"
)

// Role identifies the author of a message.
type Role string

const (
	// RoleSystem messages are text-only.
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Content is a single part of a message.
type Content interface {
	Kind() string
}

// Text is plain text content.
type Text struct{ Text string }

func (Text) Kind() string { return "text" }

// Thinking is model reasoning.
type Thinking struct {
	Text      string
	Signature string
}

func (Thinking) Kind() string { return "thinking" }

// Plan is a structured list of steps a model intends to take.
type Plan struct{ Steps []string }

func (Plan) Kind() string { return "plan" }

// Image is image content, supplied either inline.
type Image struct {
	MediaType string
	Data      []byte
	URL       string
}

func (Image) Kind() string { return "image" }

// Audio is audio content supplied inline.
type Audio struct {
	MediaType string
	Data      []byte
}

func (Audio) Kind() string { return "audio" }

// ToolUse is a model request to invoke a tool.
type ToolUse struct {
	ID        string
	Name      string
	Input     json.RawMessage
	Signature string
}

func (ToolUse) Kind() string { return "tool_use" }

// ToolContent is the subset of Content a tool result may carry.
type ToolContent interface {
	Content
	toolContent()
}

func (Text) toolContent()  {}
func (Image) toolContent() {}
func (Audio) toolContent() {}

// ToolResult is the outcome of executing a ToolUse, referenced by ToolUseID.
type ToolResult struct {
	ToolUseID string
	Name      string
	Content   []ToolContent
	IsError   bool
}

func (ToolResult) Kind() string { return "tool_result" }

// Text returns the concatenation of the result's Text blocks.
func (r ToolResult) Text() string {
	var b strings.Builder
	for _, c := range r.Content {
		if t, ok := c.(Text); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

// ToolText is the single-block result a string-returning tool produces.
func ToolText(s string) []ToolContent { return []ToolContent{Text{Text: s}} }
