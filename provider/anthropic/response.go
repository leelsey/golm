// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package anthropic

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/httpretry"
)

type respBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Signature string          `json:"signature"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
}

type apiResponse struct {
	ID         string      `json:"id"`
	Content    []respBlock `json:"content"`
	StopReason string      `json:"stop_reason"`
	Usage      struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`

		CacheCreation cacheCreation `json:"cache_creation"`
	} `json:"usage"`
}

type cacheCreation struct {
	Ephemeral5mInputTokens int `json:"ephemeral_5m_input_tokens"`
	Ephemeral1hInputTokens int `json:"ephemeral_1h_input_tokens"`
}

func (c cacheCreation) split(total int) (fiveMin, oneHour int) {
	if c.Ephemeral5mInputTokens < 0 || c.Ephemeral1hInputTokens < 0 {
		return 0, 0
	}
	if c.Ephemeral5mInputTokens+c.Ephemeral1hInputTokens > total {
		return 0, 0
	}
	return c.Ephemeral5mInputTokens, c.Ephemeral1hInputTokens
}

func stopReason(s string) golm.StopReason {
	switch s {
	case "end_turn", "stop_sequence":
		return golm.StopEndTurn
	case "tool_use":
		return golm.StopToolUse
	case "max_tokens":
		return golm.StopMaxTokens
	case "refusal":
		return golm.StopRefusal
	case "model_context_window_exceeded":
		return golm.StopContextOverflow
	case "pause_turn":
		return golm.StopPause
	default:
		return golm.StopOther
	}
}

func (r apiResponse) toResponse() golm.Response {
	var content []golm.Content
	for _, b := range r.Content {
		switch b.Type {
		case "text":
			content = append(content, golm.Text{Text: b.Text})
		case "thinking":
			content = append(content, golm.Thinking{Text: b.Thinking, Signature: b.Signature})
		case "tool_use":
			content = append(content, golm.ToolUse{ID: b.ID, Name: b.Name, Input: b.Input})
		}
	}
	fiveMin, oneHour := r.Usage.CacheCreation.split(r.Usage.CacheCreationInputTokens)
	return golm.Response{
		ID:         r.ID,
		Message:    golm.Message{Role: golm.RoleAssistant, Content: content},
		StopReason: stopReason(r.StopReason),

		Usage: golm.Usage{
			InputTokens:        r.Usage.InputTokens + r.Usage.CacheReadInputTokens + r.Usage.CacheCreationInputTokens,
			OutputTokens:       r.Usage.OutputTokens,
			CacheReadTokens:    r.Usage.CacheReadInputTokens,
			CacheWriteTokens:   r.Usage.CacheCreationInputTokens,
			CacheWrite5mTokens: fiveMin,
			CacheWrite1hTokens: oneHour,
		},
	}
}

func checkContent(req golm.Request) error {
	if err := golm.SystemTextOnly(req.Messages); err != nil {
		return fmt.Errorf("anthropic: %w", err)
	}
	if err := checkEffort(req); err != nil {
		return err
	}
	for _, m := range req.Messages {
		for _, c := range m.Content {
			switch v := c.(type) {
			case golm.Audio:
				return fmt.Errorf("anthropic: audio content is not supported")
			case golm.ToolResult:
				for _, p := range v.Content {
					if _, ok := p.(golm.Audio); ok {
						return fmt.Errorf("anthropic: audio content is not supported (tool %q)", v.Name)
					}
				}
			}
		}
	}
	return nil
}

// Complete runs a non-streaming completion.
func (c *Client) Complete(ctx context.Context, req golm.Request) (golm.Response, error) {
	if err := checkContent(req); err != nil {
		return golm.Response{}, err
	}
	resp, st, err := c.post(ctx, c.buildRequest(req, false))
	if err != nil {
		u := UsageFromError(err)
		u.LostCompletions = st.LostCompletions
		return golm.Response{Usage: u}, err
	}
	defer httpretry.DrainClose(resp.Body)
	var parsed apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		out := parsed.toResponse()
		out.Usage.LostCompletions = st.LostCompletions
		return out, fmt.Errorf("anthropic: decode response: %w", err)
	}
	out := parsed.toResponse()
	out.Usage.LostCompletions = st.LostCompletions
	return out, nil
}
