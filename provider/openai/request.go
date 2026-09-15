// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package openai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/leelsey/golm"
)

type apiFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type apiTool struct {
	Type     string  `json:"type"`
	Function apiFunc `json:"function"`
}

type apiToolCall struct {
	Index    *int   `json:"index,omitempty"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

type apiMessage struct {
	Role       string        `json:"role"`
	Content    any           `json:"content,omitempty"`
	ToolCalls  []apiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type audioOut struct {
	Voice  string `json:"voice"`
	Format string `json:"format"`
}

type apiRequest struct {
	Model      string       `json:"model"`
	Messages   []apiMessage `json:"messages"`
	Tools      []apiTool    `json:"tools,omitempty"`
	ToolChoice any          `json:"tool_choice,omitempty"`

	MaxTokens           int            `json:"max_tokens,omitempty"`
	MaxCompletionTokens int            `json:"max_completion_tokens,omitempty"`
	Temperature         *float64       `json:"temperature,omitempty"`
	ReasoningEffort     string         `json:"reasoning_effort,omitempty"`
	Modalities          []string       `json:"modalities,omitempty"`
	Audio               *audioOut      `json:"audio,omitempty"`
	Stream              bool           `json:"stream,omitempty"`
	StreamOptions       *streamOptions `json:"stream_options,omitempty"`
}

func userContent(cs []golm.Content) any {
	hasNonText := false
	for _, c := range cs {
		if _, ok := c.(golm.Text); !ok {
			if _, ok := c.(golm.Plan); !ok {
				hasNonText = true
			}
		}
	}
	if !hasNonText {
		var b strings.Builder
		for _, c := range cs {
			switch v := c.(type) {
			case golm.Text:
				b.WriteString(v.Text)
			case golm.Plan:
				b.WriteString(strings.Join(v.Steps, "\n"))
			}
		}
		return b.String()
	}
	parts := make([]any, 0, len(cs))
	for _, c := range cs {
		switch v := c.(type) {
		case golm.Text:
			parts = append(parts, map[string]any{"type": "text", "text": v.Text})
		case golm.Plan:
			parts = append(parts, map[string]any{"type": "text", "text": strings.Join(v.Steps, "\n")})
		case golm.Image:
			url := v.URL
			if url == "" {
				url = fmt.Sprintf("data:%s;base64,%s", v.MediaType, base64.StdEncoding.EncodeToString(v.Data))
			}
			parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
		case golm.Audio:
			if len(v.Data) == 0 {
				continue
			}
			parts = append(parts, map[string]any{"type": "input_audio", "input_audio": map[string]any{
				"data": base64.StdEncoding.EncodeToString(v.Data), "format": audioFormat(v.MediaType),
			}})
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return parts
}

func audioFormat(mediaType string) string {
	switch mediaType {
	case "audio/wav", "audio/x-wav":
		return "wav"
	case "audio/mpeg", "audio/mp3":
		return "mp3"
	default:
		if i := strings.LastIndex(mediaType, "/"); i >= 0 {
			return mediaType[i+1:]
		}
		return mediaType
	}
}

func buildMessages(req golm.Request) ([]apiMessage, error) {
	msgs := make([]apiMessage, 0, len(req.Messages)+1)
	if sys := req.System.Text(); sys != "" {
		msgs = append(msgs, apiMessage{Role: "system", Content: sys})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case golm.RoleSystem:
			if s := m.Text(); s != "" {
				msgs = append(msgs, apiMessage{Role: "system", Content: s})
			}
		case golm.RoleTool:
			for _, c := range m.Content {
				if tr, ok := c.(golm.ToolResult); ok {
					content := tr.Text()
					if tr.IsError {
						content = "Error: " + content
					}
					msgs = append(msgs, apiMessage{Role: "tool", ToolCallID: tr.ToolUseID, Content: content})
				}
			}
		case golm.RoleAssistant:
			am := apiMessage{Role: "assistant"}
			var text strings.Builder
			for _, c := range m.Content {
				switch v := c.(type) {
				case golm.Text:
					text.WriteString(v.Text)
				case golm.Plan:
					text.WriteString(strings.Join(v.Steps, "\n"))
				case golm.ToolUse:
					args := string(v.Input)
					if args == "" {
						args = "{}"
					}
					tc := apiToolCall{ID: v.ID, Type: "function"}
					tc.Function.Name = v.Name
					tc.Function.Arguments = args
					am.ToolCalls = append(am.ToolCalls, tc)
				case golm.Thinking:

				case golm.Audio:

				default:
					return nil, fmt.Errorf("openai: assistant message carries unsupported content %T", c)
				}
			}
			if text.Len() > 0 {
				am.Content = text.String()
			} else if len(am.ToolCalls) == 0 {
				am.Content = ""
			}
			msgs = append(msgs, am)
		default:
			msgs = append(msgs, apiMessage{Role: "user", Content: userContent(m.Content)})
		}
	}
	return msgs, nil
}

func thinkingWanted(m golm.ThinkingMode) bool {
	return m == golm.ThinkingAuto || m == golm.ThinkingBudget
}

func openAIEffort(e golm.Effort) string {
	switch e {
	case golm.EffortLow:
		return "low"
	case golm.EffortMedium:
		return "medium"
	default:
		return "high"
	}
}

func reasoningEffort(t golm.ThinkingConfig) string {
	switch t.Mode {
	case golm.ThinkingOff:
		return ""
	case golm.ThinkingBudget:
		switch {
		case t.Budget <= 0:
			return "medium"
		case t.Budget < 2048:
			return "low"
		case t.Budget < 8192:
			return "medium"
		default:
			return "high"
		}
	default:
		return "medium"
	}
}

// IsReasoningModel reports whether model is an OpenAI reasoning model.
func IsReasoningModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if strings.HasPrefix(m, "gpt-5") {
		return !strings.HasPrefix(m, "gpt-5-chat")
	}
	return strings.HasPrefix(m, "o1") ||
		strings.HasPrefix(m, "o3") ||
		strings.HasPrefix(m, "o4")
}

func takesCurrentCeilingName(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return IsReasoningModel(m) || strings.HasPrefix(m, "gpt-5")
}

func (c *Client) buildRequest(req golm.Request, stream bool) (apiRequest, error) {
	msgs, err := buildMessages(req)
	if err != nil {
		return apiRequest{}, err
	}
	out := apiRequest{
		Model:    req.Model,
		Messages: msgs,
		Stream:   stream,
	}

	reasoning := IsReasoningModel(req.Model)

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = golm.DefaultMaxTokens
	}
	if takesCurrentCeilingName(req.Model) {
		out.MaxCompletionTokens = maxTokens
	} else {
		out.MaxTokens = maxTokens
	}
	if reasoning && thinkingWanted(req.Thinking.Mode) {
		out.ReasoningEffort = reasoningEffort(req.Thinking)
	}

	if reasoning && req.Effort != golm.EffortNone {
		out.ReasoningEffort = openAIEffort(req.Effort)
	}
	if !reasoning && req.Temperature != nil {
		out.Temperature = req.Temperature
	}
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, apiTool{Type: "function", Function: apiFunc{
			Name: t.Name, Description: t.Description, Parameters: t.Schema,
		}})
	}
	if req.ToolChoice != "" {
		out.ToolChoice = map[string]any{"type": "function", "function": map[string]any{"name": req.ToolChoice}}
	}
	for _, mod := range req.ResponseModalities {
		if mod == "audio" {
			out.Modalities = []string{"text", "audio"}
			out.Audio = &audioOut{Voice: "alloy", Format: "wav"}
		}
	}
	if stream {
		out.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	return out, nil
}
