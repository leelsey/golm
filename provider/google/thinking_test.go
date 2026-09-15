// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package google

import (
	"testing"

	"github.com/leelsey/golm"
)

func TestBuildContentsThinkingRoundTrip(t *testing.T) {
	req := golm.Request{Messages: []golm.Message{
		golm.UserText("hi"),
		{Role: golm.RoleAssistant, Content: []golm.Content{golm.Thinking{Text: "reasoning"}}},
		golm.UserText("continue"),
	}}
	contents := buildContents(req)
	for i, c := range contents {
		if len(c.Parts) == 0 {
			t.Errorf("content %d has empty parts (Gemini would reject it)", i)
		}
	}
	foundThought := false
	for _, c := range contents {
		for _, p := range c.Parts {
			if p.Thought && p.Text == "reasoning" {
				foundThought = true
			}
		}
	}
	if !foundThought {
		t.Error("thinking block was not re-sent as a thought part")
	}
}
