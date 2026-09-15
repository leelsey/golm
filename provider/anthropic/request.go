// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/leelsey/golm"
)

type apiTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type apiMessage struct {
	Role    string `json:"role"`
	Content []any  `json:"content"`
}

type apiRequest struct {
	Model     string `json:"model"`
	MaxTokens int    `json:"max_tokens"`

	System      any            `json:"system,omitempty"`
	Messages    []apiMessage   `json:"messages"`
	Tools       []apiTool      `json:"tools,omitempty"`
	ToolChoice  any            `json:"tool_choice,omitempty"`
	Thinking    map[string]any `json:"thinking,omitempty"`
	Temperature *float64       `json:"temperature,omitempty"`

	OutputConfig map[string]any `json:"output_config,omitempty"`
	Stream       bool           `json:"stream,omitempty"`
}

func wireRole(r golm.Role) string {
	if r == golm.RoleAssistant {
		return "assistant"
	}
	return "user"
}

func contentBlocks(cs []golm.Content) []any {
	blocks := make([]any, 0, len(cs))
	for _, c := range cs {
		switch v := c.(type) {
		case golm.Text:
			if v.Text == "" {
				continue
			}
			blocks = append(blocks, map[string]any{"type": "text", "text": v.Text})
		case golm.Plan:
			blocks = append(blocks, map[string]any{"type": "text", "text": strings.Join(v.Steps, "\n")})
		case golm.Thinking:
			b := map[string]any{"type": "thinking", "thinking": v.Text}
			if v.Signature != "" {
				b["signature"] = v.Signature
			}
			blocks = append(blocks, b)
		case golm.ToolUse:
			input := v.Input
			if len(input) == 0 {
				input = json.RawMessage("{}")
			}
			blocks = append(blocks, map[string]any{"type": "tool_use", "id": v.ID, "name": v.Name, "input": input})
		case golm.ToolResult:
			var content any = toolResultContent(v)
			if content == nil {
				content = "(no output)"
			}
			b := map[string]any{"type": "tool_result", "tool_use_id": v.ToolUseID, "content": content}
			if v.IsError {
				b["is_error"] = true
			}
			blocks = append(blocks, b)
		case golm.Image:
			blocks = append(blocks, imageBlock(v))
		}
	}
	return blocks
}

func imageBlock(v golm.Image) map[string]any {
	if v.URL != "" {
		return map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": v.URL}}
	}
	return map[string]any{"type": "image", "source": map[string]any{
		"type": "base64", "media_type": v.MediaType, "data": v.Data,
	}}
}

func toolResultContent(r golm.ToolResult) any {
	image := false
	for _, c := range r.Content {
		if _, ok := c.(golm.Image); ok {
			image = true
			break
		}
	}
	if !image {
		if s := r.Text(); s != "" {
			return s
		}
		return nil
	}
	blocks := make([]any, 0, len(r.Content))
	for _, c := range r.Content {
		switch v := c.(type) {
		case golm.Text:
			if v.Text == "" {
				continue
			}
			blocks = append(blocks, map[string]any{"type": "text", "text": v.Text})
		case golm.Image:
			blocks = append(blocks, imageBlock(v))
		}
	}
	if len(blocks) == 0 {
		return nil
	}
	return blocks
}

func cacheControl(ttl golm.CacheTTL) map[string]any {
	cc := map[string]any{"type": "ephemeral"}
	if ttl != golm.CacheTTLDefault {
		cc["ttl"] = string(ttl)
	}
	return cc
}

const maxCacheBreakpoints = 4

func clampMarks(sections []golm.PromptSection, n int) {
	marks := 0
	for _, s := range sections {
		if s.Cache {
			marks++
		}
	}
	for i := range sections {
		if marks <= n {
			return
		}
		if sections[i].Cache {
			sections[i].Cache = false
			marks--
		}
	}
}

func systemBlocks(sections []golm.PromptSection, ttl golm.CacheTTL) []any {
	blocks := make([]any, 0, len(sections))
	carried, marked := "", false
	for i, s := range sections {
		text := carried + s.Text
		if i+1 < len(sections) {
			text += "\n\n"
		}
		if strings.TrimSpace(text) == "" {
			carried = text
			continue
		}
		carried = ""
		b := map[string]any{"type": "text", "text": text}
		if s.Cache {
			b["cache_control"] = cacheControl(ttl)
			marked = true
		}
		blocks = append(blocks, b)
	}
	if !marked {
		return nil
	}

	if carried != "" {
		last := blocks[len(blocks)-1].(map[string]any)
		last["text"] = last["text"].(string) + carried
	}
	return blocks
}

func cacheableBlock(t string) bool {
	switch t {
	case "text", "image", "tool_use", "tool_result", "document":
		return true
	}
	return false
}

func markLastCacheable(blocks []any, ttl golm.CacheTTL) {
	for i := len(blocks) - 1; i >= 0; i-- {
		b, ok := blocks[i].(map[string]any)
		if !ok {
			continue
		}
		t, _ := b["type"].(string)
		if !cacheableBlock(t) {
			continue
		}
		b["cache_control"] = cacheControl(ttl)
		return
	}
}

var (
	effortLadderFull    = []golm.Effort{golm.EffortLow, golm.EffortMedium, golm.EffortHigh, golm.EffortXHigh, golm.EffortMax}
	effortLadderNoXHigh = []golm.Effort{golm.EffortLow, golm.EffortMedium, golm.EffortHigh, golm.EffortMax}
	effortLadderEarly   = []golm.Effort{golm.EffortLow, golm.EffortMedium, golm.EffortHigh}
)

type effortSupport struct {
	prefix string
	rungs  []golm.Effort
}

var effortSupportTable = []effortSupport{
	{"claude-fable-5", effortLadderFull},
	{"claude-mythos-5", effortLadderFull},
	{"claude-opus-5", effortLadderFull},
	{"claude-opus-4-8", effortLadderFull},
	{"claude-opus-4-7", effortLadderFull},
	{"claude-sonnet-5", effortLadderFull},
	{"claude-opus-4-6", effortLadderNoXHigh},
	{"claude-sonnet-4-6", effortLadderNoXHigh},
	{"claude-opus-4-5", effortLadderEarly},

	{"claude-opus-4-1", nil},
	{"claude-opus-4-0", nil},
	{"claude-sonnet-4-5", nil},
	{"claude-haiku-4-5", nil},
	{"claude-sonnet-4", nil},
	{"claude-haiku-4", nil},
	{"claude-3", nil},
}

func normaliseModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndexByte(m, '.'); i >= 0 {
		m = m[i+1:]
	}
	if i := strings.IndexByte(m, '@'); i >= 0 {
		m = m[:i]
	}
	return m
}

func effortRungs(model string) (rungs []golm.Effort, known bool) {
	m := normaliseModel(model)
	best := -1
	for i, s := range effortSupportTable {
		if !strings.HasPrefix(m, s.prefix) {
			continue
		}
		if best < 0 || len(s.prefix) > len(effortSupportTable[best].prefix) {
			best = i
		}
	}
	if best < 0 {
		return effortLadderFull, false
	}
	return effortSupportTable[best].rungs, true
}

// IsEffortModel reports whether model accepts output_config.effort.
func IsEffortModel(model string) bool {
	rungs, _ := effortRungs(model)
	return len(rungs) > 0
}

func checkEffort(req golm.Request) error {
	if req.Effort == golm.EffortNone {
		return nil
	}
	rungs, known := effortRungs(req.Model)
	if len(rungs) == 0 {
		return fmt.Errorf("anthropic: model %q does not accept an effort setting; leave Request.Effort empty", req.Model)
	}
	for _, r := range rungs {
		if r == req.Effort {
			return nil
		}
	}
	if !known {
		return nil
	}
	return fmt.Errorf("anthropic: model %q does not have effort %q (accepts %v)", req.Model, req.Effort, rungs)
}

func (c *Client) buildRequest(req golm.Request, stream bool) apiRequest {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	out := apiRequest{
		Model:     req.Model,
		MaxTokens: maxTokens,
		Messages:  make([]apiMessage, 0, len(req.Messages)),
		Stream:    stream,
	}

	sections := req.System.Sections()

	bpIndex := -1
	for i, m := range req.Messages {
		if m.Role == golm.RoleSystem {
			if s := m.Text(); s != "" {
				if n := len(sections); n > 0 {
					sections[n-1].Text += "\n\n" + s
				} else {
					sections = append(sections, golm.PromptSection{Text: s})
				}
			}
		} else if blocks := contentBlocks(m.Content); len(blocks) > 0 {
			out.Messages = append(out.Messages, apiMessage{Role: wireRole(m.Role), Content: blocks})
		}
		if i+1 == req.Cache.MessagePrefix {
			bpIndex = len(out.Messages) - 1
		}
	}

	if req.Cache.MessagePrefix > len(req.Messages) {
		bpIndex = len(out.Messages) - 1
	}

	msgBreak := req.Cache.MessagePrefix > 0 && bpIndex >= 0
	budget := maxCacheBreakpoints
	if msgBreak {
		budget--
	}
	clampMarks(sections, budget)
	switch blocks := systemBlocks(sections, req.Cache.TTL); {
	case len(sections) == 0:
	case blocks != nil:
		out.System = blocks

	default:
		out.System = golm.SystemPrompt(sections).Text()
	}
	if msgBreak {
		markLastCacheable(out.Messages[bpIndex].Content, req.Cache.TTL)
	}
	for _, t := range req.Tools {
		schema := t.Schema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		out.Tools = append(out.Tools, apiTool{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	if req.ToolChoice != "" {
		out.ToolChoice = map[string]any{"type": "tool", "name": req.ToolChoice}
	}
	thinking := req.Thinking.Mode == golm.ThinkingAuto || req.Thinking.Mode == golm.ThinkingBudget
	switch req.Thinking.Mode {
	case golm.ThinkingAuto:

		out.Thinking = map[string]any{"type": "adaptive"}
	case golm.ThinkingDisabled:

		out.Thinking = map[string]any{"type": "disabled"}
	case golm.ThinkingBudget:

		budget := req.Thinking.Budget
		if budget <= 0 {
			budget = 2048
		} else if budget < 1024 {
			budget = 1024
		}
		out.Thinking = map[string]any{"type": "enabled", "budget_tokens": budget}

		if out.MaxTokens <= budget {
			out.MaxTokens = budget + defaultMaxTokens
		}
	}
	if req.Effort != golm.EffortNone {
		out.OutputConfig = map[string]any{"effort": string(req.Effort)}
	}

	if !thinking && req.Temperature != nil {
		out.Temperature = req.Temperature
	}
	return out
}
