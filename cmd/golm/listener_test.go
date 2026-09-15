// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

// Nothing here serves TLS, so the warning is about EXPOSURE, not only about authentication.
func TestOpenListenerWarnings(t *testing.T) {
	cases := []struct {
		name, addr, token string
		want              string
	}{
		{"loopback, no token", "127.0.0.1:8000", "", ""},
		{"loopback by name", "localhost:8000", "", ""},
		{"loopback with token", "127.0.0.1:8000", "tok", ""},
		{"exposed, no token", "0.0.0.0:8000", "", "without authentication"},
		{"bare port is every interface", ":8000", "", "without authentication"},
		{"exposed with token", "0.0.0.0:8000", "tok", "clear"},
		{"named host with token", "example.com:8000", "tok", "TLS"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out strings.Builder
			warnOpenListener(&out, "serve", c.addr, c.token, "the risk")
			got := out.String()
			if c.want == "" {
				if got != "" {
					t.Errorf("unexpected warning: %q", got)
				}
				return
			}
			if !strings.Contains(got, c.want) {
				t.Errorf("warning = %q, want one mentioning %q", got, c.want)
			}
			if !strings.Contains(got, c.addr) {
				t.Errorf("the warning should name the address: %q", got)
			}
		})
	}
}

// A sub-agent's working goes to stderr and the answer to stdout.
func TestOrchestrateSeparatesTheAnswerFromTheWorking(t *testing.T) {
	o := golm.NewOrchestrator(nil)
	o.StreamDelegates = true
	o.Add("main", "main", &golm.Agent{Model: "m", Provider: &twoStep{}})
	o.Add("helper", "sub", &golm.Agent{Model: "s", Provider: &sayer{text: "SUBAGENT-WORKING"}})
	if err := o.WireDelegation(nil); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	res, err := orchestrate(context.Background(), o, golm.NewSession(), "go", false, &stdout, &stderr)
	if err != nil {
		t.Fatalf("orchestrate: %v", err)
	}
	if res.Text() != "THE-ANSWER" {
		t.Errorf("result = %q", res.Text())
	}
	if !strings.Contains(stdout.String(), "THE-ANSWER") {
		t.Errorf("stdout = %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "SUBAGENT-WORKING") {
		t.Errorf("a sub-agent's working reached stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "SUBAGENT-WORKING") {
		t.Errorf("the sub-agent's working is not on stderr: %q", stderr.String())
	}

	if !strings.Contains(stderr.String(), "[helper] ↳") {
		t.Errorf("no delegation bracket on stderr: %q", stderr.String())
	}
}

func TestOrchestrateNoStreamPrintsOnce(t *testing.T) {
	o := golm.NewOrchestrator(nil)
	o.Add("main", "main", &golm.Agent{Model: "m", Provider: &sayer{text: "ONLY-ONCE"}})
	var stdout, stderr strings.Builder
	if _, err := orchestrate(context.Background(), o, golm.NewSession(), "go", true, &stdout, &stderr); err != nil {
		t.Fatalf("orchestrate: %v", err)
	}
	if n := strings.Count(stdout.String(), "ONLY-ONCE"); n != 1 {
		t.Errorf("the answer appears %d times on stdout, want 1", n)
	}
}

type sayer struct{ text string }

func (s *sayer) Name() string                    { return "sayer" }
func (s *sayer) Capabilities() golm.Capabilities { return golm.Capabilities{Streaming: true} }
func (s *sayer) Complete(context.Context, golm.Request) (golm.Response, error) {
	return golm.Response{Message: golm.AssistantText(s.text), StopReason: golm.StopEndTurn}, nil
}
func (s *sayer) Stream(ctx context.Context, req golm.Request, fn func(golm.StreamEvent) error) (golm.Response, error) {
	resp, _ := s.Complete(ctx, req)
	_ = fn(golm.StreamEvent{Type: golm.EventTextDelta, Text: s.text})
	return resp, nil
}

type twoStep struct{ calls int }

func (d *twoStep) Name() string { return "two-step" }
func (d *twoStep) Capabilities() golm.Capabilities {
	return golm.Capabilities{Streaming: true, Tools: true}
}
func (d *twoStep) Complete(context.Context, golm.Request) (golm.Response, error) {
	d.calls++
	if d.calls == 1 {
		return golm.Response{Message: golm.Message{Role: golm.RoleAssistant, Content: []golm.Content{
			golm.ToolUse{ID: "h", Name: "helper", Input: json.RawMessage(`{"task":"look"}`)},
		}}, StopReason: golm.StopToolUse}, nil
	}
	return golm.Response{Message: golm.AssistantText("THE-ANSWER"), StopReason: golm.StopEndTurn}, nil
}
func (d *twoStep) Stream(ctx context.Context, req golm.Request, fn func(golm.StreamEvent) error) (golm.Response, error) {
	resp, err := d.Complete(ctx, req)
	if err == nil {
		if txt := resp.Message.Text(); txt != "" {
			_ = fn(golm.StreamEvent{Type: golm.EventTextDelta, Text: txt})
		}
	}
	return resp, err
}
