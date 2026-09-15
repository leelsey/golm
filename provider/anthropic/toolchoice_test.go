// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/leelsey/golm"
)

func TestBuildRequestToolChoice(t *testing.T) {
	c := New("k")
	out := c.buildRequest(golm.Request{
		Model:     "claude",
		MaxTokens: 1024,
		Tools: []golm.ToolDef{{
			Name: "emit", Description: "d", Schema: json.RawMessage(`{"type":"object"}`),
		}},
		ToolChoice: "emit",
	}, false)

	b, _ := json.Marshal(out.ToolChoice)
	if got := string(b); got != `{"name":"emit","type":"tool"}` {
		t.Errorf("tool_choice = %s, want forced tool 'emit'", got)
	}

	auto := c.buildRequest(golm.Request{Model: "claude", MaxTokens: 10}, false)
	if auto.ToolChoice != nil {
		t.Errorf("tool_choice = %v, want nil when unset", auto.ToolChoice)
	}
}
