// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package google

import (
	"testing"

	"github.com/leelsey/golm"
)

func TestFunctionResponseNameFromToolResult(t *testing.T) {
	req := golm.Request{Messages: []golm.Message{
		golm.UserText("hi"),
		{Role: golm.RoleTool, Content: []golm.Content{
			golm.ToolResult{ToolUseID: "call_0", Name: "lookup", Content: golm.ToolText("ok")},
		}},
	}}
	found := false
	for _, c := range buildContents(req) {
		for _, p := range c.Parts {
			if p.FunctionResponse != nil && p.FunctionResponse.Name == "lookup" {
				found = true
			}
		}
	}
	if !found {
		t.Error("functionResponse.name not resolved from ToolResult.Name after compaction")
	}
}
