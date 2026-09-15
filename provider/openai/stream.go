// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/internal/sse"
)

type toolAcc struct {
	id      string
	name    string
	args    strings.Builder
	started bool
}

// Stream runs a streaming completion, normalising OpenAI SSE chunks.
func (c *Client) Stream(ctx context.Context, req golm.Request, fn func(golm.StreamEvent) error) (golm.Response, error) {
	if err := golm.ToolResultTextOnly(req.Messages); err != nil {
		return golm.Response{}, fmt.Errorf("openai: %w", err)
	}
	if err := golm.SystemTextOnly(req.Messages); err != nil {
		return golm.Response{}, fmt.Errorf("openai: %w", err)
	}
	body, err := c.buildRequest(req, true)
	if err != nil {
		return golm.Response{}, err
	}
	resp, st, err := c.post(ctx, body)
	if err != nil {
		return golm.Response{Usage: golm.Usage{LostCompletions: st.LostCompletions}}, err
	}
	defer resp.Body.Close()

	scanner := sse.NewScanner(resp.Body)
	var text, thinking strings.Builder
	tools := map[int]*toolAcc{}
	order := []int{}
	refused := false
	out := golm.Response{Message: golm.Message{Role: golm.RoleAssistant}}
	out.Usage.LostCompletions = st.LostCompletions

	var wire apiUsage

	for {
		ev, serr := scanner.Next()
		if serr == io.EOF {
			break
		}
		if serr != nil {
			return out, serr
		}
		if ev.Data == "" || ev.Data == "[DONE]" {
			if ev.Data == "[DONE]" {
				break
			}
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string        `json:"content"`
					ToolCalls []apiToolCall `json:"tool_calls"`

					ReasoningContent string `json:"reasoning_content"`
					Reasoning        string `json:"reasoning"`
					Thinking         string `json:"thinking"`
					Refusal          string `json:"refusal"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *apiUsage `json:"usage"`
			Error *struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(ev.Data), &chunk); err != nil {
			return out, err
		}
		if chunk.Error != nil {
			return out, fmt.Errorf("openai: stream error: %s: %s",
				chunk.Error.Type, golm.Redact(chunk.Error.Message, c.apiKey))
		}
		if chunk.Usage != nil {
			wire = mergeAPIUsage(wire, *chunk.Usage)
			lost := out.Usage.LostCompletions
			out.Usage = wire.toUsage()
			out.Usage.LostCompletions = lost
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		ch := chunk.Choices[0]
		if th := firstNonEmpty(ch.Delta.ReasoningContent, ch.Delta.Reasoning, ch.Delta.Thinking); th != "" {
			thinking.WriteString(th)
			if err := fn(golm.StreamEvent{Type: golm.EventThinkingDelta, Text: th}); err != nil {
				return out, err
			}
		}
		if ch.Delta.Refusal != "" {
			text.WriteString(ch.Delta.Refusal)
			refused = true
			if err := fn(golm.StreamEvent{Type: golm.EventTextDelta, Text: ch.Delta.Refusal}); err != nil {
				return out, err
			}
		}
		if ch.Delta.Content != "" {
			text.WriteString(ch.Delta.Content)
			if err := fn(golm.StreamEvent{Type: golm.EventTextDelta, Text: ch.Delta.Content}); err != nil {
				return out, err
			}
		}
		for _, tc := range ch.Delta.ToolCalls {
			idx := 0
			if tc.Index != nil {
				idx = *tc.Index
			}
			acc := tools[idx]
			if acc == nil {
				acc = &toolAcc{}
				tools[idx] = acc
				order = append(order, idx)
			}

			if tc.ID != "" && !acc.started {
				acc.id = tc.ID
			}
			if tc.Function.Name != "" {
				acc.name = tc.Function.Name
			}

			if tc.Function.Arguments != "" {
				acc.args.WriteString(tc.Function.Arguments)
			}

			if !acc.started && acc.name != "" {
				acc.started = true
				if acc.id == "" {
					acc.id = fmt.Sprintf("call_%d", idx)
				}
				if err := fn(golm.StreamEvent{Type: golm.EventToolStart, ToolID: acc.id, ToolName: acc.name}); err != nil {
					return out, err
				}
				if acc.args.Len() > 0 {
					if err := fn(golm.StreamEvent{Type: golm.EventToolDelta, ToolArgs: acc.args.String()}); err != nil {
						return out, err
					}
				}
			} else if acc.started && tc.Function.Arguments != "" {
				if err := fn(golm.StreamEvent{Type: golm.EventToolDelta, ToolArgs: tc.Function.Arguments}); err != nil {
					return out, err
				}
			}
		}
		if ch.FinishReason != "" {
			out.StopReason = stopReason(ch.FinishReason)
		}
	}

	if refused {
		out.StopReason = golm.StopRefusal
	}

	if out.StopReason == "" {
		return out, fmt.Errorf("openai: stream ended unexpectedly (no finish_reason): %w", golm.ErrStreamIncomplete)
	}

	if thinking.Len() > 0 {
		out.Message.Content = append(out.Message.Content, golm.Thinking{Text: thinking.String()})
	}
	if text.Len() > 0 {
		out.Message.Content = append(out.Message.Content, golm.Text{Text: text.String()})
	}
	for _, idx := range order {
		acc := tools[idx]
		if acc.name == "" {
			continue
		}
		if acc.id == "" {
			acc.id = fmt.Sprintf("call_%d", idx)
		}
		input := json.RawMessage(acc.args.String())
		if len(input) == 0 {
			input = json.RawMessage("{}")
		}
		out.Message.Content = append(out.Message.Content, golm.ToolUse{ID: acc.id, Name: acc.name, Input: input})
		if err := fn(golm.StreamEvent{Type: golm.EventToolStop, ToolID: acc.id}); err != nil {
			return out, err
		}
	}
	if err := fn(golm.StreamEvent{Type: golm.EventDone, Usage: &out.Usage}); err != nil {
		return out, err
	}
	return out, nil
}
