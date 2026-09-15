// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package google

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/httpretry"
)

type apiResponse struct {
	Candidates []struct {
		Content      apiContent `json:"content"`
		FinishReason string     `json:"finishReason"`
	} `json:"candidates"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata struct {
		PromptTokenCount        int `json:"promptTokenCount"`
		CandidatesTokenCount    int `json:"candidatesTokenCount"`
		ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
		CachedContentTokenCount int `json:"cachedContentTokenCount"`

		ToolUsePromptTokenCount int `json:"toolUsePromptTokenCount"`
	} `json:"usageMetadata"`
}

func (r apiResponse) usageWith(st httpretry.Stats) golm.Usage {
	u := r.usage()
	u.LostCompletions = st.LostCompletions
	return u
}

func (r apiResponse) usage() golm.Usage {
	m := r.UsageMetadata

	return golm.Usage{
		InputTokens:     m.PromptTokenCount + m.ToolUsePromptTokenCount,
		OutputTokens:    m.CandidatesTokenCount,
		ThinkingTokens:  m.ThoughtsTokenCount,
		CacheReadTokens: m.CachedContentTokenCount,
	}
}

func finishReason(s string, hasTool bool) golm.StopReason {
	switch s {
	case "STOP", "":

		if hasTool {
			return golm.StopToolUse
		}
		return golm.StopEndTurn
	case "MAX_TOKENS":
		return golm.StopMaxTokens
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY", "LANGUAGE":

		return golm.StopRefusal
	default:
		if hasTool {
			return golm.StopToolUse
		}
		return golm.StopOther
	}
}

func inlineToContent(d *apiInlineData) golm.Content {
	raw, err := base64.StdEncoding.DecodeString(d.Data)
	if err != nil || len(raw) == 0 {
		return nil
	}
	if strings.HasPrefix(d.MimeType, "audio/") {
		return golm.Audio{MediaType: d.MimeType, Data: raw}
	}
	return golm.Image{MediaType: d.MimeType, Data: raw}
}

// Complete runs a non-streaming completion.
func (c *Client) Complete(ctx context.Context, req golm.Request) (golm.Response, error) {
	if err := golm.SystemTextOnly(req.Messages); err != nil {
		return golm.Response{}, fmt.Errorf("google: %w", err)
	}
	if err := golm.ToolResultTextOnly(req.Messages); err != nil {
		return golm.Response{}, fmt.Errorf("google: %w", err)
	}
	resp, st, err := c.post(ctx, req.Model, "generateContent", "", c.buildRequest(req))
	if err != nil {
		return golm.Response{Usage: golm.Usage{LostCompletions: st.LostCompletions}}, err
	}
	defer httpretry.DrainClose(resp.Body)
	var parsed apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return golm.Response{Usage: parsed.usageWith(st)}, fmt.Errorf("google: decode response: %w", err)
	}
	if len(parsed.Candidates) == 0 {
		if fb := parsed.PromptFeedback; fb != nil && fb.BlockReason != "" {
			return golm.Response{Usage: parsed.usageWith(st)}, fmt.Errorf("google: prompt blocked: %s", fb.BlockReason)
		}
		if e := parsed.Error; e != nil && e.Message != "" {
			return golm.Response{Usage: parsed.usageWith(st)},
				fmt.Errorf("google: %s: %s", e.Status, golm.Redact(e.Message, c.apiKey))
		}
		return golm.Response{Usage: parsed.usageWith(st)}, fmt.Errorf("google: empty candidates")
	}
	cand := parsed.Candidates[0]
	var content []golm.Content
	hasTool := false
	callIdx := 0
	var thinking strings.Builder
	var text strings.Builder
	var thoughtSig string
	for _, p := range cand.Content.Parts {
		switch {
		case p.FunctionCall != nil:
			hasTool = true
			id := p.FunctionCall.ID
			if id == "" {
				id = fmt.Sprintf("call_%d", callIdx)
			}
			callIdx++
			input := p.FunctionCall.Args
			if len(input) == 0 {
				input = json.RawMessage("{}")
			}
			content = append(content, golm.ToolUse{ID: id, Name: p.FunctionCall.Name, Input: input, Signature: p.ThoughtSignature})
		case p.InlineData != nil:
			if c := inlineToContent(p.InlineData); c != nil {
				content = append(content, c)
			}
		case p.Thought && p.Text != "":
			thinking.WriteString(p.Text)
			if p.ThoughtSignature != "" {
				thoughtSig = p.ThoughtSignature
			}
		case p.Text != "":
			text.WriteString(p.Text)
		}
	}
	var head []golm.Content
	if thinking.Len() > 0 {
		head = append(head, golm.Thinking{Text: thinking.String(), Signature: thoughtSig})
	}
	if text.Len() > 0 {
		head = append(head, golm.Text{Text: text.String()})
	}
	content = append(head, content...)
	return golm.Response{
		Message:    golm.Message{Role: golm.RoleAssistant, Content: content},
		StopReason: finishReason(cand.FinishReason, hasTool),
		Usage:      parsed.usageWith(st),
	}, nil
}
