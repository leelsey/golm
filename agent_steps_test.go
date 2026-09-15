// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"testing"
)

// A caller could only report the LIMIT it set.
func TestAgentReportsStepsTaken(t *testing.T) {
	for _, tc := range []struct {
		name  string
		steps int
		max   int
		want  int
	}{
		{"concludes early", 2, 8, 2},
		{"one shot", 1, 8, 1},
		{"runs to the ceiling", 99, 3, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			p := &scriptedProvider{reply: func() (Response, error) {
				calls++
				if calls >= tc.steps {
					return Response{Message: AssistantText("done"), StopReason: StopEndTurn}, nil
				}
				return Response{
					Message: Message{Role: RoleAssistant, Content: []Content{
						ToolUse{ID: "t", Name: "noop", Input: []byte(`{}`)},
					}},
					StopReason: StopToolUse,
				}, nil
			}}
			reg := NewRegistry()
			reg.Register(NewTool("noop", "does nothing", []byte(`{"type":"object"}`),
				func(context.Context, json.RawMessage) (string, error) { return "ok", nil }))
			a := &Agent{Provider: p, Model: "m", Tools: reg, MaxSteps: tc.max}
			res, err := a.Run(context.Background(), NewSession(), "go")
			if err != nil && tc.want != tc.max {
				t.Fatalf("Run: %v", err)
			}
			if res.Steps != tc.want {
				t.Errorf("Steps = %d, want %d — the caller can only report the lease without this",
					res.Steps, tc.want)
			}
		})
	}
}

type scriptedProvider struct{ reply func() (Response, error) }

func (scriptedProvider) Name() string                                           { return "scripted" }
func (scriptedProvider) Capabilities() Capabilities                             { return Capabilities{Tools: true} }
func (p *scriptedProvider) Complete(context.Context, Request) (Response, error) { return p.reply() }
func (p *scriptedProvider) Stream(_ context.Context, _ Request, _ func(StreamEvent) error) (Response, error) {
	return p.reply()
}
