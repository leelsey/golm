// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

type erringProvider struct {
	first Response
	calls int
}

func (p *erringProvider) Name() string               { return "erring" }
func (p *erringProvider) Capabilities() Capabilities { return Capabilities{Tools: true} }
func (p *erringProvider) Complete(context.Context, Request) (Response, error) {
	p.calls++
	if p.calls == 1 {
		return p.first, nil
	}
	return Response{}, fmt.Errorf("provider boom")
}
func (p *erringProvider) Stream(ctx context.Context, req Request, _ func(StreamEvent) error) (Response, error) {
	return p.Complete(ctx, req)
}

func TestProviderErrorReturnsLastMessage(t *testing.T) {
	first := Response{
		Message: Message{Role: RoleAssistant, Content: []Content{
			Text{Text: "working"},
			ToolUse{ID: "t1", Name: "echo", Input: json.RawMessage("{}")},
		}},
		StopReason: StopToolUse,
	}
	reg := NewRegistry()
	reg.Register(echoTool())
	a := &Agent{Provider: &erringProvider{first: first}, Model: "x", Tools: reg}

	res, err := a.Run(context.Background(), NewSession(), "hi")
	if err == nil {
		t.Fatal("expected provider error")
	}
	if res.Text() != "working" {
		t.Errorf("returned message = %q, want partial last message %q", res.Text(), "working")
	}
}
