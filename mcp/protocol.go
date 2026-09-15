// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

// Package mcp implements the Model Context Protocol.
package mcp

import (
	"encoding/base64"
	"encoding/json"

	"github.com/leelsey/golm"
)

// ProtocolVersion is the MCP spec revision this implementation prefers.
const ProtocolVersion = "2025-11-25"

var supportedVersions = []string{ProtocolVersion, "2025-06-18"}

func versionSupported(v string) bool {
	for _, s := range supportedVersions {
		if s == v {
			return true
		}
	}
	return false
}

// Implementation identifies a client or server.
type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ToolsCapability advertises tool support.
type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ServerCapabilities is the server's advertised capability set.
type ServerCapabilities struct {
	Tools *ToolsCapability `json:"tools,omitempty"`
}

// ClientCapabilities is the client's advertised capability set.
type ClientCapabilities struct{}

// InitializeParams is sent by the client to begin a session.
type InitializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      Implementation     `json:"clientInfo"`
}

// InitializeResult is the server's handshake response.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      Implementation     `json:"serverInfo"`
}

// Tool is an MCP tool definition.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ListToolsResult is the tools/list response.
type ListToolsResult struct {
	Tools      []Tool `json:"tools"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// CallToolParams is the tools/call request.
type CallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// Content is one block of a tool result.
type Content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
}

// CallToolResult is the tools/call response.
type CallToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

func textResult(s string, isErr bool) CallToolResult {
	return CallToolResult{Content: []Content{{Type: "text", Text: s}}, IsError: isErr}
}

func blockResult(blocks []golm.ToolContent, isErr bool) CallToolResult {
	out := make([]Content, 0, len(blocks))
	for _, b := range blocks {
		switch c := b.(type) {
		case golm.Text:
			out = append(out, Content{Type: "text", Text: c.Text})
		case golm.Image:
			if len(c.Data) == 0 {
				out = append(out, Content{Type: "text", Text: c.URL})
				continue
			}
			out = append(out, Content{Type: "image", MimeType: c.MediaType, Data: base64.StdEncoding.EncodeToString(c.Data)})
		case golm.Audio:
			out = append(out, Content{Type: "audio", MimeType: c.MediaType, Data: base64.StdEncoding.EncodeToString(c.Data)})
		}
	}
	return CallToolResult{Content: out, IsError: isErr}
}
