// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

// ThinkingAuto means "the model decides".
func TestThinkingAutoSendsAdaptive(t *testing.T) {
	c := New("k")
	out := c.buildRequest(golm.Request{
		Model:     "claude-opus-5",
		Messages:  []golm.Message{golm.UserText("hi")},
		MaxTokens: 4096,
		Thinking:  golm.ThinkingConfig{Mode: golm.ThinkingAuto},
	}, false)

	got, _ := json.Marshal(out.Thinking)
	if string(got) != `{"type":"adaptive"}` {
		t.Errorf("thinking = %s, want {\"type\":\"adaptive\"} — budget_tokens is rejected on every current model", got)
	}
	if out.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d, want 4096 left alone — adaptive has no budget to reserve against",
			out.MaxTokens)
	}
}

// An explicit budget is a ceiling the caller asked for.
func TestThinkingBudgetKeepsTheBudgetForm(t *testing.T) {
	c := New("k")
	out := c.buildRequest(golm.Request{
		Model:     "claude-opus-4-6",
		Messages:  []golm.Message{golm.UserText("hi")},
		MaxTokens: 1000,
		Thinking:  golm.ThinkingConfig{Mode: golm.ThinkingBudget, Budget: 8000},
	}, false)

	got, _ := json.Marshal(out.Thinking)
	if string(got) != `{"budget_tokens":8000,"type":"enabled"}` {
		t.Errorf("thinking = %s, want the enabled+budget form", got)
	}

	if out.MaxTokens <= 8000 {
		t.Errorf("MaxTokens = %d, want headroom above the 8000 budget", out.MaxTokens)
	}
}

// Off must send nothing at all.
func TestThinkingOffSendsNoField(t *testing.T) {
	c := New("k")
	out := c.buildRequest(golm.Request{
		Model:    "claude-opus-5",
		Messages: []golm.Message{golm.UserText("hi")},
		Thinking: golm.ThinkingConfig{Mode: golm.ThinkingOff},
	}, false)
	if out.Thinking != nil {
		t.Errorf("thinking = %v, want the field omitted", out.Thinking)
	}
}

// Effort goes inside output_config.
func TestEffortIsNestedInOutputConfig(t *testing.T) {
	c := New("k")
	out := c.buildRequest(golm.Request{
		Model:     "claude-opus-5",
		Messages:  []golm.Message{golm.UserText("hi")},
		MaxTokens: 4096,
		Effort:    golm.EffortLow,
	}, false)

	got, _ := json.Marshal(out.OutputConfig)
	if string(got) != `{"effort":"low"}` {
		t.Errorf("output_config = %s, want {\"effort\":\"low\"}", got)
	}
	body, _ := json.Marshal(out)
	if !strings.Contains(string(body), `"output_config":{"effort":"low"}`) {
		t.Errorf("effort is not on the wire:\n%s", body)
	}
}

// Unset means unset: the field must not appear at all.
func TestNoEffortSendsNoOutputConfig(t *testing.T) {
	c := New("k")
	out := c.buildRequest(golm.Request{
		Model:    "claude-haiku-4-5",
		Messages: []golm.Message{golm.UserText("hi")},
	}, false)
	if out.OutputConfig != nil {
		t.Errorf("output_config = %v, want absent", out.OutputConfig)
	}
	body, _ := json.Marshal(out)
	if strings.Contains(string(body), "output_config") {
		t.Errorf("output_config is on the wire:\n%s", body)
	}
}
