// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import "strings"

// PromptSection is one span of the system prompt.
type PromptSection struct {
	Text string

	Cache bool
}

// SystemPrompt is the system prompt as an ordered list of sections.
type SystemPrompt []PromptSection

// Add appends texts as sections, skipping empty ones.
func (p SystemPrompt) Add(texts ...string) SystemPrompt {
	out := append(make(SystemPrompt, 0, len(p)+len(texts)), p...)
	for _, t := range texts {
		if t != "" {
			out = append(out, PromptSection{Text: t})
		}
	}
	return out
}

// Break marks a breakpoint after the last section added so far.
func (p SystemPrompt) Break() SystemPrompt {
	if len(p) == 0 {
		return p
	}
	out := append(make(SystemPrompt, 0, len(p)), p...)
	out[len(out)-1].Cache = true
	return out
}

// Sections returns the prompt as it reaches the wire.
func (p SystemPrompt) Sections() []PromptSection {
	out := make([]PromptSection, 0, len(p))
	for _, s := range p {
		if s.Text != "" {
			out = append(out, s)
			continue
		}
		if s.Cache && len(out) > 0 {
			out[len(out)-1].Cache = true
		}
	}
	return out
}

// Text is Sections joined by a blank line.
func (p SystemPrompt) Text() string {
	parts := make([]string, 0, len(p))
	for _, s := range p {
		if s.Text != "" {
			parts = append(parts, s.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// Cached reports whether any section asks for a breakpoint.
func (p SystemPrompt) Cached() bool {
	for _, s := range p {
		if s.Cache {
			return true
		}
	}
	return false
}

func (p SystemPrompt) uncached() SystemPrompt {
	if !p.Cached() {
		return p
	}
	out := append(make(SystemPrompt, 0, len(p)), p...)
	for i := range out {
		out[i].Cache = false
	}
	return out
}
