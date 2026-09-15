// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package google

import (
	"encoding/json"
	"testing"

	"github.com/leelsey/golm"
)

func TestBuildRequestToolChoice(t *testing.T) {
	c := New("k")
	out := c.buildRequest(golm.Request{
		Model: "gemini-2.5-flash",
		Tools: []golm.ToolDef{{
			Name: "emit", Description: "d", Schema: json.RawMessage(`{"type":"object"}`),
		}},
		ToolChoice: "emit",
	})

	if out.ToolConfig == nil {
		t.Fatal("toolConfig is nil, want forced function calling")
	}
	fc := out.ToolConfig.FunctionCallingConfig
	if fc.Mode != "ANY" {
		t.Errorf("mode = %q, want ANY", fc.Mode)
	}
	if len(fc.AllowedFunctionNames) != 1 || fc.AllowedFunctionNames[0] != "emit" {
		t.Errorf("allowedFunctionNames = %v, want [emit]", fc.AllowedFunctionNames)
	}

	auto := c.buildRequest(golm.Request{Model: "gemini-2.5-flash"})
	if auto.ToolConfig != nil {
		t.Errorf("toolConfig = %v, want nil when unset", auto.ToolConfig)
	}
}
