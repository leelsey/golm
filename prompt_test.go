// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"strings"
	"testing"
)

func TestPromptTextJoinsWithABlankLineAndDropsEmptySections(t *testing.T) {
	p := SystemPrompt{{Text: "identity"}, {Text: ""}, {Text: "memory"}}
	if got, want := p.Text(), "identity\n\nmemory"; got != want {
		t.Errorf("Text() = %q, want %q", got, want)
	}
	if got := (SystemPrompt{}).Text(); got != "" {
		t.Errorf("Text() of an empty prompt = %q, want empty", got)
	}
}

// The copy is the contract.
func TestPromptSectionsIsACopy(t *testing.T) {
	p := SystemPrompt{{Text: "identity", Cache: true}, {Text: "memory"}}
	secs := p.Sections()
	secs[0].Text = "clobbered"
	secs[0].Cache = false
	secs[1].Text += "\n\nfolded"

	if p[0].Text != "identity" || !p[0].Cache || p[1].Text != "memory" {
		t.Errorf("mutating Sections() reached the receiver: %#v", p)
	}
}

func TestPromptSectionsMoveADroppedMarkBack(t *testing.T) {
	p := SystemPrompt{{Text: "identity"}, {Text: "", Cache: true}, {Text: "memory"}}
	secs := p.Sections()
	if len(secs) != 2 {
		t.Fatalf("got %d sections, want 2: %#v", len(secs), secs)
	}
	if secs[0].Text != "identity" || !secs[0].Cache {
		t.Errorf("the dropped section's mark did not move back: %#v", secs)
	}
	if secs[1].Cache {
		t.Error("the mark moved forward, caching the volatile tier")
	}

	lead := SystemPrompt{{Text: "", Cache: true}, {Text: "memory"}}.Sections()
	if len(lead) != 1 || lead[0].Cache {
		t.Errorf("a leading empty section's mark was carried forward: %#v", lead)
	}
}

func TestPromptSectionsKeepWhitespaceOnlySections(t *testing.T) {
	p := SystemPrompt{{Text: "identity"}, {Text: "   "}, {Text: "memory"}}
	secs := p.Sections()
	if len(secs) != 3 || secs[1].Text != "   " {
		t.Fatalf("a whitespace-only section was dropped: %#v", secs)
	}
	if got, want := p.Text(), "identity\n\n   \n\nmemory"; got != want {
		t.Errorf("Text() = %q, want %q", got, want)
	}
}

func TestPromptAddAndBreakLeaveTheReceiverAlone(t *testing.T) {
	base := SystemPrompt{}.Add("identity")
	marked := base.Break()
	extended := base.Add("", "memory", "")

	if len(base) != 1 || base[0].Cache {
		t.Errorf("the receiver changed: %#v", base)
	}
	if len(marked) != 1 || !marked[0].Cache {
		t.Errorf("Break() = %#v, want the last section marked", marked)
	}

	if len(extended) != 2 || extended[0].Cache || extended[1].Text != "memory" {
		t.Errorf("Add() = %#v, want identity then memory, unmarked", extended)
	}
}

func TestPromptBreakOnAnEmptyPromptIsANoOp(t *testing.T) {
	var p SystemPrompt
	if got := p.Break(); len(got) != 0 {
		t.Errorf("Break() on an empty prompt = %#v, want empty", got)
	}
	if got := (SystemPrompt{}).Break(); got.Cached() {
		t.Error("Break() on an empty prompt invented a breakpoint")
	}
}

// Text and Sections are two renderings of one prompt, sent to different providers.
func TestPromptRenderingsCannotDesynchronise(t *testing.T) {
	for _, p := range []SystemPrompt{
		nil,
		{},
		{{Text: "only"}},
		{{Text: "identity", Cache: true}, {Text: "project"}, {Text: "memory"}},
		{{Text: "identity"}, {Text: "", Cache: true}, {Text: "memory"}},
		{{Text: ""}, {Text: "   ", Cache: true}, {Text: "tail"}},
		{{Text: "", Cache: true}},
	} {
		parts := make([]string, 0, len(p))
		for _, s := range p.Sections() {
			parts = append(parts, s.Text)
		}
		if got, want := strings.Join(parts, "\n\n"), p.Text(); got != want {
			t.Errorf("Sections() joined = %q but Text() = %q, for %#v", got, want, p)
		}
	}
}
