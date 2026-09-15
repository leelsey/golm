// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Message is a single turn in a conversation.
type Message struct {
	Role    Role
	Content []Content
}

// UserText builds a user message from a plain string.
func UserText(s string) Message {
	return Message{Role: RoleUser, Content: []Content{Text{Text: s}}}
}

// AssistantText builds an assistant message from a plain string.
func AssistantText(s string) Message {
	return Message{Role: RoleAssistant, Content: []Content{Text{Text: s}}}
}

// Text returns the concatenation of all Text blocks in the message.
func (m Message) Text() string {
	var b strings.Builder
	for _, c := range m.Content {
		if t, ok := c.(Text); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

// SystemTextOnly returns an error naming the first non-Text content block found in a RoleSystem message.
func SystemTextOnly(msgs []Message) error {
	for _, m := range msgs {
		if m.Role != RoleSystem {
			continue
		}
		for _, c := range m.Content {
			if _, ok := c.(Text); !ok {
				return fmt.Errorf("system messages are text-only (got %T)", c)
			}
		}
	}
	return nil
}

// ToolUses returns every ToolUse block in the message.
func (m Message) ToolUses() []ToolUse {
	var out []ToolUse
	for _, c := range m.Content {
		if t, ok := c.(ToolUse); ok {
			out = append(out, t)
		}
	}
	return out
}

type contentEnvelope struct {
	Kind      string            `json:"kind"`
	Text      string            `json:"text,omitempty"`
	Signature string            `json:"signature,omitempty"`
	Steps     []string          `json:"steps,omitempty"`
	MediaType string            `json:"media_type,omitempty"`
	Data      []byte            `json:"data,omitempty"`
	URL       string            `json:"url,omitempty"`
	ID        string            `json:"id,omitempty"`
	Name      string            `json:"name,omitempty"`
	Input     json.RawMessage   `json:"input,omitempty"`
	ToolUseID string            `json:"tool_use_id,omitempty"`
	Content   []contentEnvelope `json:"content,omitempty"`
	IsError   bool              `json:"is_error,omitempty"`
}

func toolResultEnvelope(r ToolResult) contentEnvelope {
	e := contentEnvelope{Kind: r.Kind(), ToolUseID: r.ToolUseID, Name: r.Name, IsError: r.IsError}
	if len(r.Content) == 0 {
		return e
	}
	if len(r.Content) == 1 {
		if t, ok := r.Content[0].(Text); ok {
			e.Text = t.Text
			return e
		}
	}
	e.Content = make([]contentEnvelope, 0, len(r.Content))
	for _, c := range r.Content {
		e.Content = append(e.Content, toolContentEnvelope(c))
	}
	return e
}

func toolContentEnvelope(c ToolContent) contentEnvelope {
	e := contentEnvelope{Kind: c.Kind()}
	switch v := c.(type) {
	case Text:
		e.Text = v.Text
	case Image:
		e.MediaType, e.Data, e.URL = v.MediaType, v.Data, v.URL
	case Audio:
		e.MediaType, e.Data = v.MediaType, v.Data
	}
	return e
}

func toolContentFrom(e contentEnvelope) (ToolContent, error) {
	switch e.Kind {
	case "text":
		return Text{Text: e.Text}, nil
	case "image":
		return Image{MediaType: e.MediaType, Data: e.Data, URL: e.URL}, nil
	case "audio":
		return Audio{MediaType: e.MediaType, Data: e.Data}, nil
	}
	return nil, fmt.Errorf("golm: %q cannot appear inside a tool result", e.Kind)
}

// ToolResultTextOnly returns an error naming the first non-Text block found in a tool result.
func ToolResultTextOnly(msgs []Message) error {
	for _, m := range msgs {
		for _, c := range m.Content {
			tr, ok := c.(ToolResult)
			if !ok {
				continue
			}
			for _, p := range tr.Content {
				if _, ok := p.(Text); !ok {
					return fmt.Errorf("golm: tool results are text-only on this provider (tool %q returned %s)", tr.Name, p.Kind())
				}
			}
		}
	}
	return nil
}

// MarshalJSON encodes a Message in GoLM's canonical format.
func (m Message) MarshalJSON() ([]byte, error) {
	envs := make([]contentEnvelope, 0, len(m.Content))
	for _, c := range m.Content {
		var e contentEnvelope
		e.Kind = c.Kind()
		switch v := c.(type) {
		case Text:
			e.Text = v.Text
		case Thinking:
			e.Text, e.Signature = v.Text, v.Signature
		case Plan:
			e.Steps = v.Steps
		case Image:
			e.MediaType, e.Data, e.URL = v.MediaType, v.Data, v.URL
		case Audio:
			e.MediaType, e.Data = v.MediaType, v.Data
		case ToolUse:
			input := v.Input
			if len(input) == 0 || !json.Valid(input) {
				input = json.RawMessage("{}")
			}
			e.ID, e.Name, e.Input, e.Signature = v.ID, v.Name, input, v.Signature
		case ToolResult:
			e = toolResultEnvelope(v)
		default:
			return nil, fmt.Errorf("golm: unknown content kind %q", c.Kind())
		}
		envs = append(envs, e)
	}
	return json.Marshal(struct {
		Role    Role              `json:"role"`
		Content []contentEnvelope `json:"content"`
	}{m.Role, envs})
}

// UnmarshalJSON decodes a Message from GoLM's canonical format.
func (m *Message) UnmarshalJSON(b []byte) error {
	var raw struct {
		Role    Role              `json:"role"`
		Content []contentEnvelope `json:"content"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	m.Role = raw.Role
	m.Content = make([]Content, 0, len(raw.Content))
	for _, e := range raw.Content {
		switch e.Kind {
		case "text":
			m.Content = append(m.Content, Text{Text: e.Text})
		case "thinking":
			m.Content = append(m.Content, Thinking{Text: e.Text, Signature: e.Signature})
		case "plan":
			m.Content = append(m.Content, Plan{Steps: e.Steps})
		case "image":
			m.Content = append(m.Content, Image{MediaType: e.MediaType, Data: e.Data, URL: e.URL})
		case "audio":
			m.Content = append(m.Content, Audio{MediaType: e.MediaType, Data: e.Data})
		case "tool_use":
			m.Content = append(m.Content, ToolUse{ID: e.ID, Name: e.Name, Input: e.Input, Signature: e.Signature})
		case "tool_result":
			tr := ToolResult{ToolUseID: e.ToolUseID, Name: e.Name, IsError: e.IsError}
			if len(e.Content) == 0 {
				if e.Text != "" {
					tr.Content = ToolText(e.Text)
				}
			} else {
				tr.Content = make([]ToolContent, 0, len(e.Content))
				for _, inner := range e.Content {
					c, err := toolContentFrom(inner)
					if err != nil {
						return err
					}
					tr.Content = append(tr.Content, c)
				}
			}
			m.Content = append(m.Content, tr)
		default:
			return fmt.Errorf("golm: unknown content kind %q", e.Kind)
		}
	}
	return nil
}
