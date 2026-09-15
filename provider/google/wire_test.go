// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package google

import (
	"encoding/json"
	"testing"

	"github.com/leelsey/golm"
)

func build(req golm.Request) apiRequest { return New("k").buildRequest(req) }

// A system message is merged into systemInstruction and must NOT also appear as a turn.
func TestSystemMessageBecomesInstructionNotATurn(t *testing.T) {
	out := build(golm.Request{Messages: []golm.Message{
		{Role: golm.RoleSystem, Content: []golm.Content{golm.Text{Text: "be terse"}}},
		golm.UserText("hi"),
	}})
	if out.SystemInstruction == nil || out.SystemInstruction.Parts[0].Text != "be terse" {
		t.Fatalf("systemInstruction = %+v", out.SystemInstruction)
	}
	if len(out.Contents) != 1 || out.Contents[0].Role != "user" {
		t.Fatalf("contents = %+v, want only the user turn", out.Contents)
	}
}

// Several system messages, and a SystemPrompt alongside them, concatenate in order with a blank line between.
func TestSystemMessagesConcatenateInOrder(t *testing.T) {
	req := golm.Request{Messages: []golm.Message{
		{Role: golm.RoleSystem, Content: []golm.Content{golm.Text{Text: "first"}}},
		{Role: golm.RoleSystem, Content: []golm.Content{golm.Text{Text: "second"}}},
		golm.UserText("hi"),
	}}
	req.System = req.System.Add("zeroth")
	out := build(req)
	if got := out.SystemInstruction.Parts[0].Text; got != "zeroth\n\nfirst\n\nsecond" {
		t.Errorf("systemInstruction = %q", got)
	}
}

// A failed tool must reach Gemini as an error, not as a result.
func TestErrorToolResultIsSentAsAnError(t *testing.T) {
	out := build(golm.Request{Messages: []golm.Message{
		golm.UserText("go"),
		{Role: golm.RoleTool, Content: []golm.Content{
			golm.ToolResult{ToolUseID: "1", Name: "f", Content: []golm.ToolContent{golm.Text{Text: "boom"}}, IsError: true},
		}},
	}})
	fr := out.Contents[len(out.Contents)-1].Parts[0].FunctionResponse
	if fr == nil {
		t.Fatal("no functionResponse on the wire")
	}
	if _, ok := fr.Response["error"]; !ok {
		t.Errorf("response = %v, want an \"error\" key", fr.Response)
	}
	if _, ok := fr.Response["result"]; ok {
		t.Error("a failed tool was also reported as a result")
	}
}

// Gemini rejects a Content whose parts array is empty.
func TestTurnsThatRenderToNothingAreDropped(t *testing.T) {
	out := build(golm.Request{Messages: []golm.Message{
		golm.UserText("hi"),
		{Role: golm.RoleTool, Content: []golm.Content{golm.Text{Text: "not a tool result"}}},
		{Role: golm.RoleUser, Content: []golm.Content{golm.Image{MediaType: "image/png"}}},
	}})
	for i, c := range out.Contents {
		if len(c.Parts) == 0 {
			t.Errorf("contents[%d] has an empty parts array; Gemini rejects the request", i)
		}
	}
	if len(out.Contents) != 1 {
		t.Errorf("contents = %d, want only the one turn that renders", len(out.Contents))
	}
}

// A tool call the model made with no arguments must be resent as {}.
func TestEmptyToolArgumentsBecomeAnObject(t *testing.T) {
	out := build(golm.Request{Messages: []golm.Message{
		golm.UserText("go"),
		{Role: golm.RoleAssistant, Content: []golm.Content{golm.ToolUse{ID: "1", Name: "f"}}},
	}})
	fc := out.Contents[len(out.Contents)-1].Parts[0].FunctionCall
	if fc == nil {
		t.Fatal("no functionCall on the wire")
	}
	if string(fc.Args) != "{}" {
		t.Errorf("args = %q, want {}", string(fc.Args))
	}
	if !json.Valid(fc.Args) {
		t.Error("args are not valid JSON; the whole request is rejected")
	}
}

// An assistant turn carrying plain text and a plan renders both.
func TestAssistantTextAndPlanAreRendered(t *testing.T) {
	out := build(golm.Request{Messages: []golm.Message{
		golm.UserText("go"),
		{Role: golm.RoleAssistant, Content: []golm.Content{
			golm.Text{Text: "here is the plan"},
			golm.Plan{Steps: []string{"one", "two"}},
		}},
	}})
	parts := out.Contents[len(out.Contents)-1].Parts
	if len(parts) != 2 || parts[0].Text != "here is the plan" {
		t.Fatalf("parts = %+v", parts)
	}
	if parts[1].Text != "one\ntwo" {
		t.Errorf("plan rendered as %q, want the steps joined", parts[1].Text)
	}
}

// A user turn carrying an image reaches the wire as inlineData, base64-encoded.
func TestUserImageBecomesInlineData(t *testing.T) {
	out := build(golm.Request{Messages: []golm.Message{
		{Role: golm.RoleUser, Content: []golm.Content{
			golm.Text{Text: "what is this"},
			golm.Image{MediaType: "image/png", Data: []byte{0x89, 0x50}},
		}},
	}})
	parts := out.Contents[0].Parts
	if len(parts) != 2 || parts[1].InlineData == nil {
		t.Fatalf("parts = %+v, want text plus inlineData", parts)
	}
	if parts[1].InlineData.MimeType != "image/png" || parts[1].InlineData.Data != "iVA=" {
		t.Errorf("inlineData = %+v", parts[1].InlineData)
	}
}

// Temperature is a pointer so that "unset" and "zero" are different requests.
func TestTemperatureIsOnlySentWhenSet(t *testing.T) {
	if gc := build(golm.Request{Messages: []golm.Message{golm.UserText("hi")}}).GenerationConfig; gc.Temperature != nil {
		t.Errorf("temperature sent when unset: %v", *gc.Temperature)
	}
	zero := 0.0
	gc := build(golm.Request{Messages: []golm.Message{golm.UserText("hi")}, Temperature: &zero}).GenerationConfig
	if gc.Temperature == nil || *gc.Temperature != 0 {
		t.Errorf("temperature = %v, want an explicit 0", gc.Temperature)
	}
}

// Thinking: auto asks for thoughts without a budget.
func TestThinkingConfigPerMode(t *testing.T) {
	msg := []golm.Message{golm.UserText("hi")}
	auto := build(golm.Request{Messages: msg, Thinking: golm.ThinkingConfig{Mode: golm.ThinkingAuto}}).GenerationConfig
	if auto.ThinkingConfig == nil || !auto.ThinkingConfig.IncludeThoughts {
		t.Fatalf("auto: thinkingConfig = %+v", auto.ThinkingConfig)
	}
	if auto.ThinkingConfig.ThinkingBudget != nil {
		t.Error("auto sent a budget it was never given")
	}

	bud := build(golm.Request{Messages: msg,
		Thinking: golm.ThinkingConfig{Mode: golm.ThinkingBudget, Budget: 512}}).GenerationConfig
	if bud.ThinkingConfig == nil || bud.ThinkingConfig.ThinkingBudget == nil || *bud.ThinkingConfig.ThinkingBudget != 512 {
		t.Fatalf("budget: thinkingConfig = %+v", bud.ThinkingConfig)
	}

	for _, mode := range []golm.ThinkingMode{golm.ThinkingOff, golm.ThinkingDisabled} {
		gc := build(golm.Request{Messages: msg, Thinking: golm.ThinkingConfig{Mode: mode}}).GenerationConfig
		if gc.ThinkingConfig != nil {
			t.Errorf("%v sent a thinkingConfig; a zero budget is rejected by Pro models", mode)
		}
	}
}

// An unrecognised finish reason alongside a tool call is a tool call, not StopOther.
func TestUnknownFinishReasonWithAToolCallStopsForTools(t *testing.T) {
	if got := finishReason("SOMETHING_NEW", true); got != golm.StopToolUse {
		t.Errorf("finishReason = %v, want StopToolUse", got)
	}
	if got := finishReason("SOMETHING_NEW", false); got != golm.StopOther {
		t.Errorf("finishReason = %v, want StopOther", got)
	}
}
