// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/httpretry"
)

func stopReason(s string) golm.StopReason {
	switch s {
	case "stop":
		return golm.StopEndTurn
	case "tool_calls":
		return golm.StopToolUse
	case "length":
		return golm.StopMaxTokens
	case "content_filter":

		return golm.StopRefusal
	default:
		return golm.StopOther
	}
}

type apiResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		Message struct {
			Content   string        `json:"content"`
			ToolCalls []apiToolCall `json:"tool_calls"`
			Audio     *struct {
				Data       string `json:"data"`
				Transcript string `json:"transcript"`
			} `json:"audio"`

			ReasoningContent string `json:"reasoning_content"`
			Reasoning        string `json:"reasoning"`
			Thinking         string `json:"thinking"`

			Refusal string `json:"refusal"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage apiUsage `json:"usage"`
}

type apiUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`

		CacheWriteTokens int `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func mergeAPIUsage(prev, next apiUsage) apiUsage {
	pick := func(a, b int) int {
		if b > a {
			return b
		}
		return a
	}
	out := prev
	out.PromptTokens = pick(prev.PromptTokens, next.PromptTokens)
	out.CompletionTokens = pick(prev.CompletionTokens, next.CompletionTokens)
	out.PromptTokensDetails.CachedTokens = pick(prev.PromptTokensDetails.CachedTokens, next.PromptTokensDetails.CachedTokens)
	out.PromptTokensDetails.CacheWriteTokens = pick(prev.PromptTokensDetails.CacheWriteTokens, next.PromptTokensDetails.CacheWriteTokens)
	out.CompletionTokensDetails.ReasoningTokens = pick(prev.CompletionTokensDetails.ReasoningTokens, next.CompletionTokensDetails.ReasoningTokens)
	return out
}

func (u apiUsage) toUsage() golm.Usage {
	out := u.CompletionTokens - u.CompletionTokensDetails.ReasoningTokens
	if out < 0 {
		out = 0
	}
	return golm.Usage{
		InputTokens:      u.PromptTokens,
		OutputTokens:     out,
		ThinkingTokens:   u.CompletionTokensDetails.ReasoningTokens,
		CacheReadTokens:  u.PromptTokensDetails.CachedTokens,
		CacheWriteTokens: u.PromptTokensDetails.CacheWriteTokens,
	}
}

// Complete runs a non-streaming completion.
func (c *Client) Complete(ctx context.Context, req golm.Request) (golm.Response, error) {
	if err := golm.ToolResultTextOnly(req.Messages); err != nil {
		return golm.Response{}, fmt.Errorf("openai: %w", err)
	}
	if err := golm.SystemTextOnly(req.Messages); err != nil {
		return golm.Response{}, fmt.Errorf("openai: %w", err)
	}
	body, err := c.buildRequest(req, false)
	if err != nil {
		return golm.Response{}, err
	}
	resp, st, err := c.post(ctx, body)
	if err != nil {
		return golm.Response{Usage: golm.Usage{LostCompletions: st.LostCompletions}}, err
	}
	defer httpretry.DrainClose(resp.Body)
	var parsed apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		u := parsed.Usage.toUsage()
		u.LostCompletions = st.LostCompletions
		return golm.Response{Usage: u}, fmt.Errorf("openai: decode response: %w", err)
	}

	if len(parsed.Choices) == 0 {
		u := parsed.Usage.toUsage()
		u.LostCompletions = st.LostCompletions
		return golm.Response{Usage: u}, fmt.Errorf("openai: empty choices")
	}
	ch := parsed.Choices[0]
	var content []golm.Content

	if th := firstNonEmpty(ch.Message.ReasoningContent, ch.Message.Reasoning, ch.Message.Thinking); th != "" {
		content = append(content, golm.Thinking{Text: th})
	}
	if ch.Message.Content != "" {
		content = append(content, golm.Text{Text: ch.Message.Content})
	}

	if ch.Message.Refusal != "" {
		content = append(content, golm.Text{Text: ch.Message.Refusal})
	}
	if ch.Message.Audio != nil {
		if raw, err := base64.StdEncoding.DecodeString(ch.Message.Audio.Data); err == nil && len(raw) > 0 {
			content = append(content, golm.Audio{MediaType: "audio/wav", Data: raw})
		}
		if ch.Message.Audio.Transcript != "" {
			content = append(content, golm.Text{Text: ch.Message.Audio.Transcript})
		}
	}
	for _, tc := range ch.Message.ToolCalls {
		input := json.RawMessage(tc.Function.Arguments)
		if len(input) == 0 {
			input = json.RawMessage("{}")
		}
		content = append(content, golm.ToolUse{ID: tc.ID, Name: tc.Function.Name, Input: input})
	}
	u := parsed.Usage.toUsage()
	u.LostCompletions = st.LostCompletions
	sr := stopReason(ch.FinishReason)
	if ch.Message.Refusal != "" {
		sr = golm.StopRefusal
	}
	return golm.Response{
		ID:         parsed.ID,
		Message:    golm.Message{Role: golm.RoleAssistant, Content: content},
		StopReason: sr,
		Usage:      u,
	}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
