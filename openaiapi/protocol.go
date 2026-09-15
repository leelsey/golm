// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

// Package openaiapi serves a golm agent as if it were an OpenAI model.
package openaiapi

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/leelsey/golm"
)

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`

	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`

	Session string `json:"golm_session,omitempty"`

	Tools      json.RawMessage `json:"tools,omitempty"`
	Functions  json.RawMessage `json:"functions,omitempty"`
	ToolChoice json.RawMessage `json:"tool_choice,omitempty"`
	N          *int            `json:"n,omitempty"`

	Temperature *float64 `json:"temperature,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	User        string   `json:"user,omitempty"`
}

func (r *chatRequest) unsupported() string {
	switch {
	case len(r.Tools) > 0 && string(r.Tools) != "null":
		return "this endpoint serves an agent, which runs its own tools; it cannot call tools supplied by the caller"
	case len(r.Functions) > 0 && string(r.Functions) != "null":
		return "this endpoint serves an agent, which runs its own tools; the deprecated functions parameter is not supported"
	case len(r.ToolChoice) > 0 && string(r.ToolChoice) != "null":
		return "tool_choice has no meaning here: the agent chooses its own tools"
	case r.N != nil && *r.N != 1:
		return "n must be 1: an agent produces one answer, not a set of samples"
	}
	return ""
}

type chatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Name    string          `json:"name,omitempty"`
}

func (m chatMessage) text() string {
	if len(m.Content) == 0 || string(m.Content) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(m.Content, &s); err == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(m.Content, &parts); err != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" || p.Text != "" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

type chatResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
	Usage   *chatUsage   `json:"usage,omitempty"`
}

type chatChoice struct {
	Index        int         `json:"index"`
	Message      *outMessage `json:"message,omitempty"`
	Delta        *outMessage `json:"delta,omitempty"`
	FinishReason *string     `json:"finish_reason"`
}

type outMessage struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`

	CompletionTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details,omitempty"`
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details,omitempty"`
}

func usageOf(u golm.Usage) *chatUsage {
	c := &chatUsage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens + u.ThinkingTokens,
		TotalTokens:      u.Total(),
	}
	if u.ThinkingTokens > 0 {
		c.CompletionTokensDetails = &struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		}{ReasoningTokens: u.ThinkingTokens}
	}
	if u.CacheReadTokens > 0 {
		c.PromptTokensDetails = &struct {
			CachedTokens int `json:"cached_tokens"`
		}{CachedTokens: u.CacheReadTokens}
	}
	return c
}

func finishReason(r golm.StopReason) string {
	switch r {
	case golm.StopMaxTokens:
		return "length"
	case golm.StopToolUse:
		return "tool_calls"
	default:
		return "stop"
	}
}

type modelList struct {
	Object string      `json:"object"`
	Data   []modelInfo `json:"data"`
}

type modelInfo struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	Created     int64  `json:"created"`
	OwnedBy     string `json:"owned_by"`
	Description string `json:"golm_description,omitempty"`
}

type apiError struct {
	Error apiErrorBody `json:"error"`
}

type apiErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
	Param   string `json:"param,omitempty"`
}

func errorBody(kind, code, format string, args ...any) apiError {
	return apiError{Error: apiErrorBody{
		Message: fmt.Sprintf(format, args...), Type: kind, Code: code,
	}}
}
