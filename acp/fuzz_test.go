// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package acp_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/leelsey/golm/acp"
)

// SessionNotification.Update is `any` over DISTINCT variant structs.
func FuzzUpdateVariantsKeepTheirShape(f *testing.F) {
	f.Add("hello", "call-1", "running", "a title")
	f.Add("", "", "", "")
	f.Add("\n\t multi\nline", "id/with/slashes", "completed", "t")
	f.Add(strings.Repeat("x", 4096), "c", "failed", "t")
	f.Add("emoji 🧪 and 한국어", "c", "pending", "t")

	f.Fuzz(func(t *testing.T, text, callID, status, title string) {
		chunks := []struct {
			name string
			v    any
			disc string
		}{
			{"agent_message", acp.AgentMessage(text), acp.UpdateAgentMessageChunk},
			{"agent_thought", acp.AgentThought(text), acp.UpdateAgentThoughtChunk},
			{"user_message", acp.UserMessage(text), acp.UpdateUserMessageChunk},
		}
		for _, c := range chunks {
			raw := marshalUpdate(t, c.v)
			if got := raw["sessionUpdate"]; got != c.disc {
				t.Fatalf("%s: sessionUpdate = %v, want %q", c.name, got, c.disc)
			}

			if _, ok := raw["content"].(map[string]any); !ok {
				t.Fatalf("%s: content is %T, want a ContentBlock object", c.name, raw["content"])
			}
		}

		start := acp.ToolCallStart{
			SessionUpdate: acp.UpdateToolCall, ToolCallID: callID,
			Title: title, Status: status, Kind: "other",
		}
		raw := marshalUpdate(t, start)
		if raw["sessionUpdate"] != acp.UpdateToolCall {
			t.Fatalf("tool_call: sessionUpdate = %v", raw["sessionUpdate"])
		}
		if _, ok := raw["toolCallId"]; !ok {
			t.Fatal("tool_call: no toolCallId; the editor cannot match the update to the call")
		}

		prog := acp.ToolCallProgress{
			SessionUpdate: acp.UpdateToolCallUpdate, ToolCallID: callID,
			Status: status, Content: acp.ToolOutput(text),
		}
		raw = marshalUpdate(t, prog)
		if raw["sessionUpdate"] != acp.UpdateToolCallUpdate {
			t.Fatalf("tool_call_update: sessionUpdate = %v", raw["sessionUpdate"])
		}
		if c, ok := raw["content"]; ok {
			if _, isArray := c.([]any); !isArray {
				t.Fatalf("tool_call_update: content is %T, want a []ToolCallContent array", c)
			}
		} else if strings.TrimSpace(text) != "" && text != "" {
			if acp.ToolOutput(text) != nil {
				t.Fatal("tool_call_update: content was dropped although there was output")
			}
		}
	})
}

func marshalUpdate(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(acp.SessionNotification{SessionID: "s", Update: v})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out struct {
		SessionID string         `json:"sessionId"`
		Update    map[string]any `json:"update"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, b)
	}
	if out.SessionID != "s" {
		t.Fatalf("sessionId lost: %s", b)
	}
	if out.Update == nil {
		t.Fatalf("update decoded to nothing: %s", b)
	}
	return out.Update
}
