// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/internal/sse"
)

type blockAcc struct {
	typ       string
	text      strings.Builder
	signature string
	toolID    string
	toolName  string
	toolJSON  strings.Builder
}

func (a *blockAcc) content() golm.Content {
	switch a.typ {
	case "text":
		if a.text.Len() == 0 {
			return nil
		}
		return golm.Text{Text: a.text.String()}
	case "thinking":
		return golm.Thinking{Text: a.text.String(), Signature: a.signature}
	case "tool_use":
		input := json.RawMessage(a.toolJSON.String())
		if len(input) == 0 || !json.Valid(input) {
			input = json.RawMessage("{}")
		}
		return golm.ToolUse{ID: a.toolID, Name: a.toolName, Input: input}
	default:
		return nil
	}
}

// Stream runs a streaming completion, normalising Anthropic SSE events.
func (c *Client) Stream(ctx context.Context, req golm.Request, fn func(golm.StreamEvent) error) (golm.Response, error) {
	if err := checkContent(req); err != nil {
		return golm.Response{}, err
	}
	resp, st, err := c.post(ctx, c.buildRequest(req, true))
	if err != nil {
		u := UsageFromError(err)
		u.LostCompletions = st.LostCompletions
		return golm.Response{Usage: u}, err
	}
	defer resp.Body.Close()

	scanner := sse.NewScanner(resp.Body)
	blocks := map[int]*blockAcc{}
	maxIdx := -1
	out := golm.Response{Message: golm.Message{Role: golm.RoleAssistant}}
	out.Usage.LostCompletions = st.LostCompletions

	var fresh, cacheRead, cacheWrite int

	var cc cacheCreation

	sawStop := false
	for {
		ev, serr := scanner.Next()
		if serr == io.EOF {
			if !sawStop && out.StopReason == "" {
				return out, fmt.Errorf("anthropic: stream ended unexpectedly (no stop_reason or message_stop): %w", golm.ErrStreamIncomplete)
			}
			break
		}
		if serr != nil {
			return out, serr
		}
		if ev.Data == "" {
			continue
		}
		switch ev.Type {
		case "error":
			var e struct {
				Error struct {
					Type    string `json:"type"`
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.Unmarshal([]byte(ev.Data), &e)
			return out, fmt.Errorf("anthropic: stream error: %s: %s", e.Error.Type, e.Error.Message)
		case "content_block_start":
			var e struct {
				Index        int `json:"index"`
				ContentBlock struct {
					Type string `json:"type"`
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"content_block"`
			}
			if err := json.Unmarshal([]byte(ev.Data), &e); err != nil {
				return out, err
			}

			acc := blocks[e.Index]
			if acc == nil {
				acc = &blockAcc{}
				blocks[e.Index] = acc
			}
			acc.typ = e.ContentBlock.Type
			acc.toolID = e.ContentBlock.ID
			acc.toolName = e.ContentBlock.Name
			if e.Index > maxIdx {
				maxIdx = e.Index
			}
			if acc.typ == "tool_use" {
				if err := fn(golm.StreamEvent{Type: golm.EventToolStart, ToolID: acc.toolID, ToolName: acc.toolName}); err != nil {
					return out, err
				}
			}
		case "content_block_delta":
			var e struct {
				Index int `json:"index"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					Thinking    string `json:"thinking"`
					Signature   string `json:"signature"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			if err := json.Unmarshal([]byte(ev.Data), &e); err != nil {
				return out, err
			}
			acc := blocks[e.Index]
			if acc == nil {
				switch e.Delta.Type {
				case "text_delta":
					acc = &blockAcc{typ: "text"}
				case "thinking_delta":
					acc = &blockAcc{typ: "thinking"}
				default:
					continue
				}
				blocks[e.Index] = acc
				if e.Index > maxIdx {
					maxIdx = e.Index
				}
			}
			switch e.Delta.Type {
			case "text_delta":
				acc.text.WriteString(e.Delta.Text)
				if err := fn(golm.StreamEvent{Type: golm.EventTextDelta, Text: e.Delta.Text}); err != nil {
					return out, err
				}
			case "thinking_delta":
				acc.text.WriteString(e.Delta.Thinking)
				if err := fn(golm.StreamEvent{Type: golm.EventThinkingDelta, Text: e.Delta.Thinking}); err != nil {
					return out, err
				}
			case "signature_delta":
				acc.signature += e.Delta.Signature
			case "input_json_delta":
				acc.toolJSON.WriteString(e.Delta.PartialJSON)
				if err := fn(golm.StreamEvent{Type: golm.EventToolDelta, ToolArgs: e.Delta.PartialJSON}); err != nil {
					return out, err
				}
			}
		case "content_block_stop":
			var e struct {
				Index int `json:"index"`
			}
			_ = json.Unmarshal([]byte(ev.Data), &e)
			if acc := blocks[e.Index]; acc != nil && acc.typ == "tool_use" {
				if err := fn(golm.StreamEvent{Type: golm.EventToolStop, ToolID: acc.toolID}); err != nil {
					return out, err
				}
			}
		case "message_delta":

			var e struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`

				Usage struct {
					InputTokens              *float64 `json:"input_tokens"`
					OutputTokens             *float64 `json:"output_tokens"`
					CacheCreationInputTokens *float64 `json:"cache_creation_input_tokens"`
					CacheReadInputTokens     *float64 `json:"cache_read_input_tokens"`

					CacheCreation *cacheCreation `json:"cache_creation"`
				} `json:"usage"`
			}
			if err := json.Unmarshal([]byte(ev.Data), &e); err != nil {
				return out, err
			}
			if e.Delta.StopReason != "" {
				out.StopReason = stopReason(e.Delta.StopReason)
			}
			if e.Usage.OutputTokens != nil {
				out.Usage.OutputTokens = int(*e.Usage.OutputTokens)
			}

			if e.Usage.InputTokens != nil {
				fresh = int(*e.Usage.InputTokens)
			}
			if e.Usage.CacheReadInputTokens != nil {
				cacheRead = int(*e.Usage.CacheReadInputTokens)
			}
			if e.Usage.CacheCreationInputTokens != nil {
				cacheWrite = int(*e.Usage.CacheCreationInputTokens)
			}
			if e.Usage.CacheCreation != nil {
				cc = *e.Usage.CacheCreation
			}
			out.Usage.InputTokens = fresh + cacheRead + cacheWrite
			out.Usage.CacheReadTokens = cacheRead
			out.Usage.CacheWriteTokens = cacheWrite
			out.Usage.CacheWrite5mTokens, out.Usage.CacheWrite1hTokens = cc.split(cacheWrite)
		case "message_start":
			var e struct {
				Message struct {
					Usage struct {
						InputTokens              float64       `json:"input_tokens"`
						CacheCreationInputTokens float64       `json:"cache_creation_input_tokens"`
						CacheReadInputTokens     float64       `json:"cache_read_input_tokens"`
						CacheCreation            cacheCreation `json:"cache_creation"`
					} `json:"usage"`
				} `json:"message"`
			}

			if err := json.Unmarshal([]byte(ev.Data), &e); err != nil {
				return out, fmt.Errorf("anthropic: message_start: %w", err)
			}
			u := e.Message.Usage
			fresh, cacheRead, cacheWrite = int(u.InputTokens), int(u.CacheReadInputTokens), int(u.CacheCreationInputTokens)
			cc = u.CacheCreation
			out.Usage.InputTokens = fresh + cacheRead + cacheWrite
			out.Usage.CacheReadTokens = cacheRead
			out.Usage.CacheWriteTokens = cacheWrite
			out.Usage.CacheWrite5mTokens, out.Usage.CacheWrite1hTokens = cc.split(cacheWrite)
		case "message_stop":
			sawStop = true
		}
	}

	for i := 0; i <= maxIdx; i++ {
		if acc := blocks[i]; acc != nil {
			if c := acc.content(); c != nil {
				out.Message.Content = append(out.Message.Content, c)
			}
		}
	}
	if err := fn(golm.StreamEvent{Type: golm.EventDone, Usage: &out.Usage}); err != nil {
		return out, err
	}
	return out, nil
}
