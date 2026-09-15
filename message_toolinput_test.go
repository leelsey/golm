// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"encoding/json"
	"testing"
)

func TestMarshalInvalidToolInputCoerced(t *testing.T) {
	m := Message{Role: RoleAssistant, Content: []Content{
		ToolUse{ID: "t1", Name: "echo", Input: json.RawMessage(`{"q":"go`)},
	}}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal failed on invalid tool input: %v", err)
	}
	var back Message
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	uses := back.ToolUses()
	if len(uses) != 1 || string(uses[0].Input) != "{}" {
		t.Errorf("input = %q, want {}", uses[0].Input)
	}
}
