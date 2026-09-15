// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package google

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leelsey/golm"
)

// IMAGE_SAFETY and LANGUAGE are the model declining.
func TestFinishReason_ImageSafetyAndLanguageAreRefusals(t *testing.T) {
	for _, r := range []string{"SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY", "LANGUAGE"} {
		if got := finishReason(r, false); got != golm.StopRefusal {
			t.Errorf("finishReason(%q) = %v, want StopRefusal", r, got)
		}
	}
}

// A truncation or refusal reason carried alongside a tool call must not be masked by the tool use.
func TestFinishReason_TruncationNotMaskedByToolUse(t *testing.T) {
	if got := finishReason("MAX_TOKENS", true); got != golm.StopMaxTokens {
		t.Errorf("finishReason(MAX_TOKENS, tool) = %v, want StopMaxTokens", got)
	}
	if got := finishReason("SAFETY", true); got != golm.StopRefusal {
		t.Errorf("finishReason(SAFETY, tool) = %v, want StopRefusal", got)
	}
	if got := finishReason("STOP", true); got != golm.StopToolUse {
		t.Errorf("finishReason(STOP, tool) = %v, want StopToolUse", got)
	}
}

func TestBuildRequest_SafetySettings(t *testing.T) {
	c := New("k")
	none := c.buildRequest(golm.Request{Model: "gemini-2.5-flash", Safety: golm.SafetyNone})
	if len(none.SafetySettings) != len(safetyCategories) {
		t.Fatalf("SafetyNone: got %d settings, want %d", len(none.SafetySettings), len(safetyCategories))
	}
	for _, s := range none.SafetySettings {
		if s.Threshold != "BLOCK_NONE" {
			t.Errorf("category %s threshold = %q, want BLOCK_NONE", s.Category, s.Threshold)
		}
	}
	low := c.buildRequest(golm.Request{Model: "gemini-2.5-flash", Safety: golm.SafetyLowered})
	if len(low.SafetySettings) != len(safetyCategories) {
		t.Fatalf("SafetyLowered: got %d settings, want %d", len(low.SafetySettings), len(safetyCategories))
	}
	for _, s := range low.SafetySettings {
		if s.Threshold != "BLOCK_ONLY_HIGH" {
			t.Errorf("SafetyLowered category %s threshold = %q, want BLOCK_ONLY_HIGH", s.Category, s.Threshold)
		}
	}
	def := c.buildRequest(golm.Request{Model: "gemini-2.5-flash"})
	if def.SafetySettings != nil {
		t.Errorf("SafetyDefault must leave safetySettings unset, got %+v", def.SafetySettings)
	}
}

// The PARSE direction: a Gemini thoughtSignature on a function-call part.
func TestComplete_CapturesThoughtSignature(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[`+
			`{"text":"deliberating","thought":true,"thoughtSignature":"TSIG"},`+
			`{"functionCall":{"name":"echo","args":{"x":1}},"thoughtSignature":"FSIG"}`+
			`]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":5}}`)
	}))
	defer srv.Close()

	c := New("key").WithBaseURL(srv.URL)
	resp, err := c.Complete(context.Background(), golm.Request{Model: "gemini-2.5-flash", Messages: []golm.Message{golm.UserText("hi")}})
	if err != nil {
		t.Fatal(err)
	}
	uses := resp.Message.ToolUses()
	if len(uses) != 1 || uses[0].Signature != "FSIG" {
		t.Fatalf("tool-call signature not captured: %+v", uses)
	}
	var sawThoughtSig bool
	for _, ct := range resp.Message.Content {
		if th, ok := ct.(golm.Thinking); ok && th.Signature == "TSIG" {
			sawThoughtSig = true
		}
	}
	if !sawThoughtSig {
		t.Fatalf("thought signature not captured onto Thinking: %+v", resp.Message.Content)
	}
}

// A Gemini thoughtSignature on a prior tool call must be echoed back verbatim on the same call.
func TestBuildContents_EchoesThoughtSignature(t *testing.T) {
	c := New("k")
	out := c.buildRequest(golm.Request{
		Model: "gemini-2.5-flash",
		Messages: []golm.Message{{
			Role: golm.RoleAssistant,
			Content: []golm.Content{
				golm.Thinking{Text: "deliberating", Signature: "tsig"},
				golm.ToolUse{ID: "call_1", Name: "lookup", Input: []byte(`{"q":1}`), Signature: "fsig"},
			},
		}},
	})
	var sawThought, sawCall bool
	for _, ct := range out.Contents {
		for _, p := range ct.Parts {
			if p.Thought && p.ThoughtSignature == "tsig" {
				sawThought = true
			}
			if p.FunctionCall != nil && p.ThoughtSignature == "fsig" {
				sawCall = true
			}
		}
	}
	if !sawThought {
		t.Error("thinking thoughtSignature not echoed")
	}
	if !sawCall {
		t.Error("tool-call thoughtSignature not echoed")
	}
}
