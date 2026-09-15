// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func req(name string, t Tool) ToolRequest {
	return ToolRequest{Agent: "a", Step: 1, Tool: t, Call: ToolUse{ID: "c1", Name: name, Input: json.RawMessage(`{}`)}}
}

func plainTool(name string) Tool {
	return NewTool(name, "d", json.RawMessage(`{"type":"object"}`),
		func(context.Context, json.RawMessage) (string, error) { return "ok", nil })
}

func TestPolicyRulesZeroValueAllows(t *testing.T) {
	p := &PolicyRules{}
	if d, _ := p.Decide(req("anything", plainTool("anything"))); d != DecisionAllow {
		t.Fatalf("decision = %q, want allow: a deployment with no policy must behave as before", d)
	}
}

func TestPolicyRulesOrderIsMostSpecificFirst(t *testing.T) {
	run := WithTraits(plainTool("run"), ToolTraits{Process: true})
	cases := []struct {
		name  string
		rules *PolicyRules
		tool  Tool
		want  Decision
	}{
		{"deny by name beats everything", &PolicyRules{Deny: []string{"run"}, Allow: []string{"run"}, Default: DecisionAllow}, run, DecisionDeny},
		{"ask by name beats allow by name", &PolicyRules{Ask: []string{"run"}, Allow: []string{"run"}}, run, DecisionAsk},
		{"allow by name beats a trait rule", &PolicyRules{Allow: []string{"run"}, AskTraits: []string{"process"}}, run, DecisionAllow},
		{"deny trait beats ask trait", &PolicyRules{DenyTraits: []string{"process"}, AskTraits: []string{"process"}}, run, DecisionDeny},
		{"ask trait beats default", &PolicyRules{AskTraits: []string{"process"}, Default: DecisionAllow}, run, DecisionAsk},
		{"default applies when nothing matches", &PolicyRules{Default: DecisionDeny}, run, DecisionDeny},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if d, _ := c.rules.Decide(req("run", c.tool)); d != c.want {
				t.Errorf("decision = %q, want %q", d, c.want)
			}
		})
	}
}

// A tool that declares nothing is unreviewed code.
func TestPolicyRulesUnreviewedOutranksDefault(t *testing.T) {
	p := &PolicyRules{Unreviewed: DecisionAsk, Default: DecisionAllow}
	if d, why := p.Decide(req("mystery", plainTool("mystery"))); d != DecisionAsk {
		t.Fatalf("decision = %q (%s), want ask", d, why)
	}
	declared := WithTraits(plainTool("known"), ToolTraits{ReadOnly: true})
	if d, _ := p.Decide(req("known", declared)); d != DecisionAllow {
		t.Errorf("a tool that declares its traits should not be treated as unreviewed")
	}
}

// The property that matters most.
func TestPolicyAskWithoutApproverDenies(t *testing.T) {
	p := &PolicyRules{Ask: []string{"run"}}
	_, err := p.Policy()(context.Background(), req("run", plainTool("run")))
	if !errors.Is(err, ErrToolDenied) {
		t.Fatalf("err = %v, want a denial", err)
	}
	if !errors.Is(err, ErrNoApprover) {
		t.Errorf("err = %v, should say there was nobody to ask", err)
	}
}

func TestPolicyAskConsultsTheApprover(t *testing.T) {
	var asked []string
	p := &PolicyRules{
		AskTraits: []string{"network"},
		Approve: func(_ context.Context, r ToolRequest) (bool, error) {
			asked = append(asked, r.Call.Name)
			return r.Call.Name == "fetch", nil
		},
	}
	pol := p.Policy()
	net := func(n string) Tool { return WithTraits(plainTool(n), ToolTraits{Network: true}) }

	if _, err := pol(context.Background(), req("fetch", net("fetch"))); err != nil {
		t.Errorf("approved call was refused: %v", err)
	}
	_, err := pol(context.Background(), req("post", net("post")))
	if !errors.Is(err, ErrToolDenied) || !strings.Contains(err.Error(), "operator") {
		t.Errorf("err = %v, want a refusal naming the operator", err)
	}
	if len(asked) != 2 {
		t.Errorf("approver consulted %d times, want 2", len(asked))
	}
}

func TestPolicyAuditSeesAllowedCallsToo(t *testing.T) {
	var seen []string
	p := &PolicyRules{
		Deny: []string{"run"},
		Audit: func(r ToolRequest, d Decision, err error) {
			seen = append(seen, r.Call.Name+"="+string(d))
		},
	}
	pol := p.Policy()
	_, _ = pol(context.Background(), req("read_file", plainTool("read_file")))
	_, _ = pol(context.Background(), req("run", plainTool("run")))
	want := []string{"read_file=allow", "run=deny"}
	if len(seen) != 2 || seen[0] != want[0] || seen[1] != want[1] {
		t.Errorf("audit saw %v, want %v — a record of refusals only is not a record", seen, want)
	}
}

func TestPolicyApproverHonoursACancelledRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &PolicyRules{
		Ask: []string{"run"},
		Approve: func(context.Context, ToolRequest) (bool, error) {
			t.Fatal("should not ask on a dead run")
			return true, nil
		},
	}
	if _, err := p.Policy()(ctx, req("run", plainTool("run"))); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want the cancellation", err)
	}
}

func TestPolicyConfigValidate(t *testing.T) {
	bad := []struct {
		name string
		pc   PolicyConfig
		want string
	}{
		{"unknown default", PolicyConfig{Default: "maybe"}, "want allow, deny or ask"},
		{"unknown trait", PolicyConfig{AskTraits: []string{"telepathy"}}, "unknown trait"},
		{"name in two lists", PolicyConfig{Deny: []string{"run"}, Allow: []string{"run"}}, "both"},
		{"empty entry", PolicyConfig{Ask: []string{" "}}, "empty entry"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			err := c.pc.Validate("")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want one mentioning %q", err, c.want)
			}
		})
	}
	if err := (&PolicyConfig{Default: "ASK", AskTraits: []string{"Process"}}).Validate(""); err != nil {
		t.Errorf("case should not matter: %v", err)
	}
}

func TestPolicyConfigNeedsApprover(t *testing.T) {
	for _, c := range []struct {
		pc   *PolicyConfig
		want bool
	}{
		{nil, false},
		{&PolicyConfig{Default: "deny"}, false},
		{&PolicyConfig{Default: "ask"}, true},
		{&PolicyConfig{Unreviewed: "ask"}, true},
		{&PolicyConfig{Ask: []string{"run"}}, true},
		{&PolicyConfig{AskTraits: []string{"process"}}, true},
	} {
		if got := c.pc.NeedsApprover(); got != c.want {
			t.Errorf("%+v: NeedsApprover = %v, want %v", c.pc, got, c.want)
		}
	}
}

// The gate has to hold on the path the agent actually takes, not only in Decide.
func TestPolicyRulesStopTheToolFromRunning(t *testing.T) {
	var ran bool
	dangerous := WithTraits(NewTool("run", "d", json.RawMessage(`{"type":"object"}`),
		func(context.Context, json.RawMessage) (string, error) { ran = true; return "done", nil }),
		ToolTraits{Process: true})

	reg := NewRegistry()
	reg.Register(dangerous)
	fp := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "t1", Name: "run", Input: json.RawMessage(`{}`)},
		}}, StopReason: StopToolUse},
		{Message: AssistantText("understood"), StopReason: StopEndTurn},
	}}
	rules := &PolicyRules{DenyTraits: []string{"process"}}
	a := &Agent{Provider: fp, Model: "m", Tools: reg, ToolPolicy: rules.Policy()}

	res, err := a.Run(context.Background(), NewSession(), "run something")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ran {
		t.Fatal("the denied tool executed anyway")
	}
	if res.ToolDenials != 1 {
		t.Errorf("ToolDenials = %d, want 1", res.ToolDenials)
	}
}

func TestTraitNamesAreMatchedAndValidated(t *testing.T) {
	all := WithTraits(plainTool("all"), ToolTraits{ReadOnly: true, Filesystem: true, Network: true, Process: true})
	for _, trait := range []string{"read_only", "filesystem", "network", "process", "READ_ONLY", " process "} {
		p := &PolicyRules{DenyTraits: []string{trait}}
		if d, _ := p.Decide(req("all", all)); d != DecisionDeny {
			t.Errorf("trait %q did not match a tool declaring it", trait)
		}
		if !ValidTrait(trait) {
			t.Errorf("ValidTrait(%q) = false", trait)
		}
	}

	only := WithTraits(plainTool("only"), ToolTraits{ReadOnly: true})
	for _, trait := range []string{"filesystem", "network", "process"} {
		p := &PolicyRules{DenyTraits: []string{trait}, Default: DecisionAllow}
		if d, _ := p.Decide(req("only", only)); d != DecisionAllow {
			t.Errorf("trait %q matched a tool that does not declare it", trait)
		}
	}
	if ValidTrait("telepathy") || ValidTrait("") {
		t.Error("an unknown trait was accepted")
	}
}

// A prompt waits on a PERSON.
func TestApprovalWaitHonoursContext(t *testing.T) {
	held := make(chan struct{})
	rules := &PolicyRules{
		Ask: []string{"t"},
		Approve: func(ctx context.Context, _ ToolRequest) (bool, error) {
			<-held
			return true, nil
		},
	}
	pol := rules.Policy()
	req := ToolRequest{Call: ToolUse{Name: "t", Input: json.RawMessage(`{}`)}}

	first := make(chan struct{})
	go func() { defer close(first); _, _ = pol(context.Background(), req) }()
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := pol(ctx, req); done <- err }()
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("second call returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a cancelled run could not stop waiting behind an unanswered prompt")
	}
	close(held)
	<-first
}

// A broken approver must not run the tool.
func TestPanickingApproverDeniesAndReleasesTheGate(t *testing.T) {
	rules := &PolicyRules{
		Ask:     []string{"t"},
		Approve: func(context.Context, ToolRequest) (bool, error) { panic("boom") },
	}
	pol := rules.Policy()
	req := ToolRequest{Call: ToolUse{Name: "t", Input: json.RawMessage(`{}`)}}
	for i := 0; i < 2; i++ {
		done := make(chan error, 1)
		go func() { _, err := pol(context.Background(), req); done <- err }()
		select {
		case err := <-done:
			if !errors.Is(err, ErrToolDenied) {
				t.Fatalf("call %d: got %v, want a denial", i, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("call %d hung: the gate was never released", i)
		}
	}
}

// A broken Audit must not take down a run the gate had just allowed.
func TestPanickingAuditDoesNotFailTheCall(t *testing.T) {
	rules := &PolicyRules{Audit: func(ToolRequest, Decision, error) { panic("boom") }}
	pol := rules.Policy()
	if _, err := pol(context.Background(), ToolRequest{Call: ToolUse{Name: "t"}}); err != nil {
		t.Errorf("an allowed call failed because the audit panicked: %v", err)
	}
}
