// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"errors"
)

// ErrToolDenied is the canonical refusal.
var ErrToolDenied = errors.New("golm: tool call denied by policy")

// ToolTraits is what a tool does when it runs.
type ToolTraits struct {
	ReadOnly   bool
	Filesystem bool
	Network    bool
	Process    bool
}

// TraitedTool is a Tool that declares its traits.
type TraitedTool interface {
	Tool
	Traits() ToolTraits
}

// TraitsOf returns t's declared traits.
func TraitsOf(t Tool) (ToolTraits, bool) {
	d, ok := t.(TraitedTool)
	if !ok {
		return ToolTraits{}, false
	}
	return d.Traits(), true
}

// WithTraits returns t declaring tr, for the tools that cannot declare for themselves.
func WithTraits(t Tool, tr ToolTraits) Tool { return traitedTool{Tool: t, traits: tr} }

type traitedTool struct {
	Tool
	traits ToolTraits
}

func (t traitedTool) Traits() ToolTraits { return t.traits }

// ToolRequest is one tool call put to a ToolPolicy, before anything runs.
type ToolRequest struct {
	Agent string

	Step int

	Tool Tool

	Call ToolUse
}

// ToolPolicy decides whether a tool call may run.
type ToolPolicy func(ctx context.Context, req ToolRequest) (context.Context, error)
