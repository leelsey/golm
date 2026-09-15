// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

// Package hermes drives models.
package hermes

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/leelsey/golm"
)

const (
	toolsOpen     = "<tools>"
	toolsClose    = "</tools>"
	callOpen      = "<tool_call>"
	callClose     = "</tool_call>"
	responseOpen  = "<tool_response>"
	responseClose = "</tool_response>"
)

const maxBufferedTag = 256 << 10

// Provider wraps another Provider, translating between golm's neutral tool types and the tag convention.
type Provider struct {
	inner golm.Provider

	Instruction string
}

var _ golm.Provider = (*Provider)(nil)

// Wrap returns p speaking the tag convention.
func Wrap(p golm.Provider) *Provider { return &Provider{inner: p} }

// Name reports the wrapped provider's name, tagged.
func (p *Provider) Name() string { return p.inner.Name() + "+hermes-tags" }

// Capabilities is the wrapped provider's, with Tools asserted.
func (p *Provider) Capabilities() golm.Capabilities {
	c := p.inner.Capabilities()
	c.Tools = true
	c.ToolResultImages = false
	return c
}

const defaultInstruction = `You may call tools. The tools available to you are listed below as JSON schemas inside <tools></tools>.

To call one, emit exactly this and nothing else on those lines:
<tool_call>
{"name": "<tool name>", "arguments": {<arguments matching that tool's schema>}}
</tool_call>

Call at most one tool per message unless more are genuinely independent. Do not describe the call in prose as well; the tags are the call. Results come back to you inside <tool_response></tool_response>. Wait for a result before assuming what it contains.`

func (p *Provider) instruction() string {
	if p.Instruction != "" {
		return p.Instruction
	}
	return defaultInstruction
}

func (p *Provider) prepare(req golm.Request) golm.Request {
	if len(req.Tools) == 0 {
		return req
	}
	out := req
	out.Tools = nil

	forced := out.ToolChoice
	out.ToolChoice = ""

	var b strings.Builder
	b.WriteString(p.instruction())
	if forced != "" {
		fmt.Fprintf(&b, "\n\nYou must call the %q tool in your next message.", forced)
	}
	b.WriteString("\n\n" + toolsOpen + "\n")
	for _, t := range req.Tools {
		line, err := json.Marshal(toolSchema{Type: "function", Function: fn{
			Name: t.Name, Description: t.Description, Parameters: t.Schema,
		}})
		if err != nil {
			continue
		}
		b.Write(line)
		b.WriteString("\n")
	}
	b.WriteString(toolsClose)

	out.System = append(append(golm.SystemPrompt{}, req.System.Sections()...),
		golm.PromptSection{Text: b.String()})
	out.Messages = tagMessages(req.Messages)
	return out
}

type toolSchema struct {
	Type     string `json:"type"`
	Function fn     `json:"function"`
}

type fn struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

func tagMessages(msgs []golm.Message) []golm.Message {
	out := make([]golm.Message, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case golm.RoleTool:
			var b strings.Builder
			for _, c := range m.Content {
				tr, ok := c.(golm.ToolResult)
				if !ok {
					continue
				}
				body := tr.Text()
				if tr.IsError {
					body = "error: " + body
				}
				fmt.Fprintf(&b, "%s\n%s\n%s\n", responseOpen, body, responseClose)
			}
			if b.Len() == 0 {
				continue
			}
			out = append(out, golm.Message{Role: golm.RoleUser,
				Content: []golm.Content{golm.Text{Text: strings.TrimRight(b.String(), "\n")}}})
		case golm.RoleAssistant:
			out = append(out, golm.Message{Role: golm.RoleAssistant,
				Content: []golm.Content{golm.Text{Text: renderAssistant(m)}}})
		default:
			out = append(out, m)
		}
	}
	return out
}

func renderAssistant(m golm.Message) string {
	var b strings.Builder
	for _, c := range m.Content {
		switch v := c.(type) {
		case golm.Text:
			b.WriteString(v.Text)
		case golm.ToolUse:
			args := string(v.Input)
			if args == "" {
				args = "{}"
			}
			fmt.Fprintf(&b, "\n%s\n{\"name\": %s, \"arguments\": %s}\n%s\n",
				callOpen, strconv.Quote(v.Name), args, callClose)
		}
	}
	return strings.TrimSpace(b.String())
}

// Complete calls the wrapped provider and parses any tool calls out of the answer.
func (p *Provider) Complete(ctx context.Context, req golm.Request) (golm.Response, error) {
	resp, err := p.inner.Complete(ctx, p.prepare(req))
	if err != nil {
		return resp, err
	}
	return p.interpret(resp, len(req.Tools) > 0, turnOf(req)), nil
}

func turnOf(req golm.Request) int { return len(req.Messages) }

func (p *Provider) interpret(resp golm.Response, hadTools bool, turn int) golm.Response {
	if !hadTools {
		return resp
	}
	text := resp.Message.Text()
	if !strings.Contains(text, callOpen) {
		return resp
	}
	content, n := parseCalls(text, turn)
	if n == 0 {
		return resp
	}
	resp.Message = golm.Message{Role: golm.RoleAssistant, Content: content}

	if resp.StopReason == golm.StopEndTurn || resp.StopReason == golm.StopOther {
		resp.StopReason = golm.StopToolUse
	}
	return resp
}

func parseCalls(text string, turn int) ([]golm.Content, int) {
	var out []golm.Content
	n := 0
	addText := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, golm.Text{Text: s})
		}
	}
	rest := text
	for {
		i := strings.Index(rest, callOpen)
		if i < 0 {
			break
		}
		j := strings.Index(rest[i:], callClose)
		if j < 0 {
			break
		}
		body := rest[i+len(callOpen) : i+j]
		use, ok := parseCall(body, turn, n)
		if !ok {
			addText(rest[:i+j+len(callClose)])
			rest = rest[i+j+len(callClose):]
			continue
		}
		addText(rest[:i])
		out = append(out, use)
		n++
		rest = rest[i+j+len(callClose):]
	}
	addText(rest)
	return out, n
}

type wireCall struct {
	Name       string          `json:"name"`
	Arguments  json.RawMessage `json:"arguments"`
	Parameters json.RawMessage `json:"parameters"`
}

func parseCall(body string, turn, idx int) (golm.ToolUse, bool) {
	body = strings.TrimSpace(body)
	var c wireCall
	if err := json.Unmarshal([]byte(body), &c); err != nil || c.Name == "" {
		return golm.ToolUse{}, false
	}
	args := c.Arguments
	if len(args) == 0 {
		args = c.Parameters
	}
	args = normaliseArgs(args)
	return golm.ToolUse{
		ID:    fmt.Sprintf("tag_%d_%d", turn, idx),
		Name:  c.Name,
		Input: args,
	}, true
}

func normaliseArgs(raw json.RawMessage) json.RawMessage {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage(`{}`)
	}

	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			if t := strings.TrimSpace(s); strings.HasPrefix(t, "{") && json.Valid([]byte(t)) {
				return json.RawMessage(t)
			}
		}
	}
	if raw[0] != '{' {
		return json.RawMessage(`{}`)
	}
	return raw
}
