// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package openai

import (
	"encoding/json"
	"testing"

	"github.com/leelsey/golm"
)

func TestBuildRequestToolChoice(t *testing.T) {
	c := New("k")
	out, err := c.buildRequest(golm.Request{
		Model: "gpt-4o",
		Tools: []golm.ToolDef{{
			Name: "emit", Description: "d", Schema: json.RawMessage(`{"type":"object"}`),
		}},
		ToolChoice: "emit",
	}, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}

	b, _ := json.Marshal(out.ToolChoice)
	if got := string(b); got != `{"function":{"name":"emit"},"type":"function"}` {
		t.Errorf("tool_choice = %s, want forced function 'emit'", got)
	}

	auto, err := c.buildRequest(golm.Request{Model: "gpt-4o"}, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if auto.ToolChoice != nil {
		t.Errorf("tool_choice = %v, want nil when unset", auto.ToolChoice)
	}
}
