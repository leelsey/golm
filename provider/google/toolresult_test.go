// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package google

import (
	"testing"

	"github.com/leelsey/golm"
)

// The refusal is under the shared guard contract; the claim is this wire format's own.
func TestToolResultImagesNotClaimed(t *testing.T) {
	if New("key").Capabilities().ToolResultImages {
		t.Error("ToolResultImages should be false while functionResponse carries text only")
	}
}

func TestToolResultTextUnchanged(t *testing.T) {
	req := golm.Request{Messages: []golm.Message{{Role: golm.RoleTool, Content: []golm.Content{
		golm.ToolResult{ToolUseID: "c0", Name: "echo", Content: golm.ToolText("echoed")},
	}}}}
	contents := buildContents(req)
	if len(contents) != 1 || len(contents[0].Parts) != 1 {
		t.Fatalf("contents = %+v", contents)
	}
	fr := contents[0].Parts[0].FunctionResponse
	if fr == nil || fr.Response["result"] != "echoed" {
		t.Errorf("functionResponse = %+v, want result \"echoed\"", fr)
	}
}
