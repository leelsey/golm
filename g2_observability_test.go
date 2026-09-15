// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestErrMaxStepsIsWrapped(t *testing.T) {
	loop := Response{
		Message:    Message{Role: RoleAssistant, Content: []Content{ToolUse{ID: "c", Name: "echo", Input: json.RawMessage(`{}`)}}},
		StopReason: StopToolUse,
	}
	resps := make([]Response, 10)
	for i := range resps {
		resps[i] = loop
	}
	reg := NewRegistry()
	reg.Register(echoTool())
	a := &Agent{Provider: &fakeProvider{responses: resps}, Model: "x", Tools: reg, MaxSteps: 3}
	res, err := a.Run(context.Background(), NewSession(), "hi")
	if !errors.Is(err, ErrMaxSteps) {
		t.Fatalf("err=%v, want errors.Is ErrMaxSteps", err)
	}
	if res.StopReason != StopToolUse {
		t.Errorf("StopReason=%q, want StopToolUse", res.StopReason)
	}
}

func TestStopReasonSurfacesRefusal(t *testing.T) {
	fp := &fakeProvider{responses: []Response{{Message: AssistantText(""), StopReason: StopRefusal}}}
	a := &Agent{Provider: fp, Model: "x"}

	res, err := a.Run(context.Background(), NewSession(), "hi")
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("run err = %v, want ErrRefused", err)
	}
	if res.StopReason != StopRefusal {
		t.Fatalf("StopReason=%q, want StopRefusal", res.StopReason)
	}
	if res.Response.StopReason != StopRefusal {
		t.Fatalf("Response.StopReason=%q, want StopRefusal", res.Response.StopReason)
	}
}

func TestNilProviderReturnsError(t *testing.T) {
	a := &Agent{Model: "x"}
	if _, err := a.Run(context.Background(), NewSession(), "hi"); !errors.Is(err, ErrNoProvider) {
		t.Fatalf("err=%v, want ErrNoProvider", err)
	}
}

func TestToolErrorCounter(t *testing.T) {
	fp := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{ToolUse{ID: "c", Name: "nope", Input: json.RawMessage(`{}`)}}}, StopReason: StopToolUse},
		{Message: AssistantText("done"), StopReason: StopEndTurn},
	}}
	var seen int
	a := &Agent{Provider: fp, Model: "x", Tools: NewRegistry(),
		OnToolResult: func(tr ToolResult) {
			if tr.IsError {
				seen++
			}
		}}
	res, err := a.Run(context.Background(), NewSession(), "hi")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.ToolErrors != 1 {
		t.Errorf("ToolErrors=%d, want 1", res.ToolErrors)
	}
	if seen != 1 {
		t.Errorf("OnToolResult saw %d errors, want 1", seen)
	}
}
