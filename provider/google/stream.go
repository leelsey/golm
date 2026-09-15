// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package google

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/internal/sse"
)

// Stream runs a streaming completion over streamGenerateContent.
func (c *Client) Stream(ctx context.Context, req golm.Request, fn func(golm.StreamEvent) error) (golm.Response, error) {
	if err := golm.SystemTextOnly(req.Messages); err != nil {
		return golm.Response{}, fmt.Errorf("google: %w", err)
	}
	if err := golm.ToolResultTextOnly(req.Messages); err != nil {
		return golm.Response{}, fmt.Errorf("google: %w", err)
	}
	resp, st, err := c.post(ctx, req.Model, "streamGenerateContent", "alt=sse", c.buildRequest(req))
	if err != nil {
		return golm.Response{Usage: golm.Usage{LostCompletions: st.LostCompletions}}, err
	}
	defer resp.Body.Close()

	scanner := sse.NewScanner(resp.Body)
	var text, thinking strings.Builder
	var thoughtSig string
	var tools []golm.ToolUse
	var media []golm.Content
	callIdx := 0
	out := golm.Response{Message: golm.Message{Role: golm.RoleAssistant}}
	out.Usage.LostCompletions = st.LostCompletions
	finish := ""

	for {
		ev, serr := scanner.Next()
		if serr == io.EOF {
			if finish == "" {
				return out, fmt.Errorf("google: stream ended unexpectedly (no finishReason): %w", golm.ErrStreamIncomplete)
			}
			break
		}
		if serr != nil {
			return out, serr
		}
		if ev.Data == "" {
			continue
		}
		var chunk apiResponse
		if err := json.Unmarshal([]byte(ev.Data), &chunk); err != nil {
			return out, err
		}

		out.Usage.Merge(golm.Usage{
			InputTokens:     chunk.UsageMetadata.PromptTokenCount + chunk.UsageMetadata.ToolUsePromptTokenCount,
			OutputTokens:    chunk.UsageMetadata.CandidatesTokenCount,
			ThinkingTokens:  chunk.UsageMetadata.ThoughtsTokenCount,
			CacheReadTokens: chunk.UsageMetadata.CachedContentTokenCount,
		})
		if chunk.Error != nil {
			return out, fmt.Errorf("google: stream error: %s: %s",
				chunk.Error.Status, golm.Redact(chunk.Error.Message, c.apiKey))
		}

		if chunk.PromptFeedback != nil && chunk.PromptFeedback.BlockReason != "" {
			return out, fmt.Errorf("google: prompt blocked: %s", chunk.PromptFeedback.BlockReason)
		}
		if len(chunk.Candidates) == 0 {
			continue
		}
		cand := chunk.Candidates[0]
		if cand.FinishReason != "" {
			finish = cand.FinishReason
		}
		for _, p := range cand.Content.Parts {
			switch {
			case p.FunctionCall != nil:
				id := p.FunctionCall.ID
				if id == "" {
					id = fmt.Sprintf("call_%d", callIdx)
				}
				callIdx++
				input := p.FunctionCall.Args
				if len(input) == 0 {
					input = json.RawMessage("{}")
				}
				tools = append(tools, golm.ToolUse{ID: id, Name: p.FunctionCall.Name, Input: input, Signature: p.ThoughtSignature})
				if err := fn(golm.StreamEvent{Type: golm.EventToolStart, ToolID: id, ToolName: p.FunctionCall.Name}); err != nil {
					return out, err
				}
				if err := fn(golm.StreamEvent{Type: golm.EventToolDelta, ToolArgs: string(input)}); err != nil {
					return out, err
				}
				if err := fn(golm.StreamEvent{Type: golm.EventToolStop, ToolID: id}); err != nil {
					return out, err
				}
			case p.InlineData != nil:
				if c := inlineToContent(p.InlineData); c != nil {
					media = append(media, c)
				}
			case p.Thought && p.Text != "":
				thinking.WriteString(p.Text)
				if p.ThoughtSignature != "" {
					thoughtSig = p.ThoughtSignature
				}
				if err := fn(golm.StreamEvent{Type: golm.EventThinkingDelta, Text: p.Text}); err != nil {
					return out, err
				}
			case p.Text != "":
				text.WriteString(p.Text)
				if err := fn(golm.StreamEvent{Type: golm.EventTextDelta, Text: p.Text}); err != nil {
					return out, err
				}
			}
		}
	}

	if thinking.Len() > 0 {
		out.Message.Content = append(out.Message.Content, golm.Thinking{Text: thinking.String(), Signature: thoughtSig})
	}
	if text.Len() > 0 {
		out.Message.Content = append(out.Message.Content, golm.Text{Text: text.String()})
	}
	out.Message.Content = append(out.Message.Content, media...)
	for _, t := range tools {
		out.Message.Content = append(out.Message.Content, t)
	}
	out.StopReason = finishReason(finish, len(tools) > 0)
	if err := fn(golm.StreamEvent{Type: golm.EventDone, Usage: &out.Usage}); err != nil {
		return out, err
	}
	return out, nil
}
