// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestMessageJSONRoundTrip(t *testing.T) {
	in := Message{
		Role: RoleAssistant,
		Content: []Content{
			Thinking{Text: "let me think", Signature: "sig123"},
			Text{Text: "hello"},
			ToolUse{ID: "t1", Name: "search", Input: json.RawMessage(`{"q":"go"}`), Signature: "tsig"},
			ToolResult{ToolUseID: "t1", Content: ToolText("result"), IsError: false},
			Plan{Steps: []string{"a", "b"}},
		},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Message
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Role != in.Role || len(out.Content) != len(in.Content) {
		t.Fatalf("round-trip mismatch: got %+v", out)
	}
	if out.Content[1].(Text).Text != "hello" {
		t.Errorf("text block lost: %+v", out.Content[1])
	}
	tu := out.Content[2].(ToolUse)
	if tu.ID != "t1" || tu.Name != "search" || !bytes.Equal(tu.Input, json.RawMessage(`{"q":"go"}`)) {
		t.Errorf("tool_use block lost: %+v", tu)
	}
	if tu.Signature != "tsig" {
		t.Errorf("tool_use signature lost: %q", tu.Signature)
	}
	if out.Content[0].(Thinking).Signature != "sig123" {
		t.Errorf("thinking signature lost")
	}
}

func TestMessageHelpers(t *testing.T) {
	m := Message{Role: RoleAssistant, Content: []Content{
		Text{Text: "a"},
		ToolUse{ID: "x", Name: "t"},
		Text{Text: "b"},
	}}
	if got := m.Text(); got != "ab" {
		t.Errorf("Text() = %q, want %q", got, "ab")
	}
	if uses := m.ToolUses(); len(uses) != 1 || uses[0].ID != "x" {
		t.Errorf("ToolUses() = %+v", uses)
	}
}

// A text tool result — every string tool's result.
func TestTextToolResultWireIsUnchanged(t *testing.T) {
	for _, c := range []struct {
		name string
		msg  Message
		want string
	}{
		{
			name: "plain",
			msg: Message{Role: RoleTool, Content: []Content{
				ToolResult{ToolUseID: "t1", Name: "search", Content: ToolText("result")},
			}},
			want: `{"role":"tool","content":[{"kind":"tool_result","text":"result","name":"search","tool_use_id":"t1"}]}`,
		},
		{
			name: "error",
			msg: Message{Role: RoleTool, Content: []Content{
				ToolResult{ToolUseID: "t3", Name: "e", Content: ToolText("boom"), IsError: true},
			}},
			want: `{"role":"tool","content":[{"kind":"tool_result","text":"boom","name":"e","tool_use_id":"t3","is_error":true}]}`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, err := json.Marshal(c.msg)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(b) != c.want {
				t.Fatalf("tool result wire changed:\n got %s\nwant %s", b, c.want)
			}
			var back Message
			if err := json.Unmarshal([]byte(c.want), &back); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			tr, ok := back.Content[0].(ToolResult)
			if !ok || tr.Text() != c.msg.Content[0].(ToolResult).Text() || tr.IsError != c.msg.Content[0].(ToolResult).IsError {
				t.Fatalf("the historic form must still decode: %+v", back.Content[0])
			}
			if len(tr.Content) != 1 {
				t.Fatalf("a \"text\" key decodes to one block, got %d", len(tr.Content))
			}
		})
	}
}

// Media is the only reason the nested array exists.
func TestToolResultWithImageRoundTrips(t *testing.T) {
	in := Message{Role: RoleTool, Content: []Content{
		ToolResult{ToolUseID: "t2", Name: "screenshot", Content: []ToolContent{
			Text{Text: "captured"},
			Image{MediaType: "image/png", Data: []byte{0x89, 'P', 'N', 'G'}},
		}},
	}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"role":"tool","content":[{"kind":"tool_result","name":"screenshot","tool_use_id":"t2",` +
		`"content":[{"kind":"text","text":"captured"},{"kind":"image","media_type":"image/png","data":"iVBORw=="}]}]}`
	if string(b) != want {
		t.Fatalf("media result wire:\n got %s\nwant %s", b, want)
	}
	var out Message
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tr, ok := out.Content[0].(ToolResult)
	if !ok || len(tr.Content) != 2 {
		t.Fatalf("blocks lost: %+v", out.Content[0])
	}
	if tr.Text() != "captured" {
		t.Errorf("text block lost: %q", tr.Text())
	}
	img, ok := tr.Content[1].(Image)
	if !ok || img.MediaType != "image/png" || !bytes.Equal(img.Data, []byte{0x89, 'P', 'N', 'G'}) {
		t.Errorf("image block lost: %+v", tr.Content[1])
	}
}

// The nesting is one level deep and leaf kinds only.
func TestNestedToolUseInAToolResultIsRejected(t *testing.T) {
	const raw = `{"role":"tool","content":[{"kind":"tool_result","tool_use_id":"t1","content":[` +
		`{"kind":"tool_use","id":"x","name":"n","input":{}}]}]}`
	var m Message
	err := json.Unmarshal([]byte(raw), &m)
	if err == nil {
		t.Fatal("a tool_use nested in a tool result must be rejected, not decoded")
	}
	if !strings.Contains(err.Error(), "tool_use") {
		t.Errorf("the error must name the offending kind, got %v", err)
	}
}

func TestToolResultTextOnlyNamesToolAndKind(t *testing.T) {
	msgs := []Message{
		UserText("hi"),
		{Role: RoleTool, Content: []Content{
			ToolResult{ToolUseID: "t1", Name: "fine", Content: ToolText("ok")},
			ToolResult{ToolUseID: "t2", Name: "screenshot", Content: []ToolContent{
				Text{Text: "captured"},
				Image{MediaType: "image/png", Data: []byte{1}},
			}},
		}},
	}
	err := ToolResultTextOnly(msgs)
	if err == nil {
		t.Fatal("an image in a tool result must fail a text-only provider")
	}
	if !strings.Contains(err.Error(), "screenshot") {
		t.Errorf("the error must name the tool, got %v", err)
	}
	if !strings.Contains(err.Error(), "image") {
		t.Errorf("the error must name the kind, got %v", err)
	}
	if err := ToolResultTextOnly(msgs[:1]); err != nil {
		t.Errorf("text-only history must pass: %v", err)
	}
}

// An empty result must stay empty across a reload.
func TestEmptyToolResultRoundTripsAsEmpty(t *testing.T) {
	in := Message{Role: RoleTool, Content: []Content{ToolResult{ToolUseID: "t1", Name: "noop"}}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Message
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tr, ok := out.Content[0].(ToolResult)
	if !ok {
		t.Fatalf("content[0] = %T, want ToolResult", out.Content[0])
	}
	if len(tr.Content) != 0 {
		t.Errorf("Content = %+v, want none — an empty result grew a block on reload", tr.Content)
	}
}
