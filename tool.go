// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type toolCallKey struct{}

// WithToolCall marks ctx as executing one tool call.
func WithToolCall(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, toolCallKey{}, id)
}

// ToolCallIDOf is the id of the call the running tool is serving, or "" outside one.
func ToolCallIDOf(ctx context.Context) string {
	id, _ := ctx.Value(toolCallKey{}).(string)
	return id
}

// Tool is an action the model can invoke.
type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Execute(ctx context.Context, input json.RawMessage) ([]ToolContent, error)
}

// ToolFunc adapts a plain function into a Tool.
type ToolFunc struct {
	NameVal        string
	DescriptionVal string
	SchemaVal      json.RawMessage
	Fn             func(ctx context.Context, input json.RawMessage) ([]ToolContent, error)
}

// NewTool builds a Tool from a name, description, JSON schema.
func NewTool(name, description string, schema json.RawMessage, fn func(ctx context.Context, input json.RawMessage) (string, error)) ToolFunc {
	return ToolFunc{NameVal: name, DescriptionVal: description, SchemaVal: schema,
		Fn: func(ctx context.Context, input json.RawMessage) ([]ToolContent, error) {
			out, err := fn(ctx, input)
			if err != nil {
				return nil, err
			}
			return ToolText(out), nil
		}}
}

// NewContentTool is NewTool for a tool whose result is not plain text.
func NewContentTool(name, description string, schema json.RawMessage, fn func(ctx context.Context, input json.RawMessage) ([]ToolContent, error)) ToolFunc {
	return ToolFunc{NameVal: name, DescriptionVal: description, SchemaVal: schema, Fn: fn}
}

// TextTool builds a Tool taking a single required string argument named argName.
func TextTool(name, description, argName, argDescription string, fn func(ctx context.Context, text string) (string, error)) ToolFunc {
	arg := map[string]any{"type": "string"}
	if argDescription != "" {
		arg["description"] = argDescription
	}
	schema, _ := json.Marshal(map[string]any{
		"type":       "object",
		"properties": map[string]any{argName: arg},
		"required":   []string{argName},
	})
	return ToolFunc{
		NameVal:        name,
		DescriptionVal: description,
		SchemaVal:      schema,
		Fn: func(ctx context.Context, input json.RawMessage) ([]ToolContent, error) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(input, &fields); err != nil {
				return nil, err
			}

			raw, ok := fields[argName]
			if !ok {
				return nil, fmt.Errorf("missing required argument %q", argName)
			}
			var text string

			if err := json.Unmarshal(raw, &text); err != nil || string(raw) == "null" {
				return nil, fmt.Errorf("argument %q must be a string", argName)
			}
			if strings.TrimSpace(text) == "" {
				return nil, fmt.Errorf("argument %q must not be empty", argName)
			}
			out, err := fn(ctx, text)
			if err != nil {
				return nil, err
			}
			return ToolText(out), nil
		},
	}
}

func (t ToolFunc) Name() string            { return t.NameVal }
func (t ToolFunc) Description() string     { return t.DescriptionVal }
func (t ToolFunc) Schema() json.RawMessage { return t.SchemaVal }
func (t ToolFunc) Execute(ctx context.Context, input json.RawMessage) ([]ToolContent, error) {
	return t.Fn(ctx, input)
}

// Registry is a concurrency-safe set of tools keyed by name.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register adds or replaces a tool.
func (r *Registry) Register(tools ...Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tools == nil {
		r.tools = make(map[string]Tool)
	}
	for _, t := range tools {
		r.tools[t.Name()] = t
	}
}

// Get returns the tool registered under name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List returns all tools sorted by name.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]Tool, 0, len(names))
	for _, n := range names {
		out = append(out, r.tools[n])
	}
	return out
}

// Defs returns provider-facing definitions for all registered tools.
func (r *Registry) Defs() []ToolDef {
	tools := r.List()
	defs := make([]ToolDef, 0, len(tools))
	for _, t := range tools {
		defs = append(defs, ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			Schema:      t.Schema(),
		})
	}
	return defs
}
