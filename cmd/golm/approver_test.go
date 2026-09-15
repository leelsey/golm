// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/leelsey/golm"
)

func scriptedApprover(answers string) (*approver, *strings.Builder) {
	var out strings.Builder
	return &approver{
		gate:   make(chan struct{}, 1),
		in:     bufio.NewReader(strings.NewReader(answers)),
		out:    &out,
		always: map[string]bool{},
	}, &out
}

func callFor(tool string) golm.ToolRequest {
	return golm.ToolRequest{Call: golm.ToolUse{ID: "1", Name: tool, Input: []byte(`{"a":1}`)}}
}

// The whole point of the gate is what it does with each answer.
func TestApproverAnswers(t *testing.T) {
	for _, c := range []struct {
		answer string
		want   bool
	}{
		{"y\n", true}, {"Y\n", true}, {"yes\n", true}, {"YES\n", true}, {" yes \n", true},
		{"a\n", true}, {"always\n", true}, {"ALWAYS\n", true},
		{"n\n", false}, {"no\n", false}, {"\n", false}, {"maybe\n", false},
		{"yolo\n", false}, {"1\n", false}, {"ye\n", false},
	} {
		a, _ := scriptedApprover(c.answer)
		got, err := a.Approve(context.Background(), callFor("run"))
		if err != nil {
			t.Errorf("answer %q: %v", c.answer, err)
		}
		if got != c.want {
			t.Errorf("answer %q allowed=%v, want %v", c.answer, got, c.want)
		}
	}
}

// A terminal that closed is not consent.
func TestApproverTreatsEOFAsNo(t *testing.T) {
	a, _ := scriptedApprover("")
	got, err := a.Approve(context.Background(), callFor("run"))
	if err != nil {
		t.Errorf("EOF produced an error: %v", err)
	}
	if got {
		t.Error("EOF at the prompt was taken as consent")
	}
}

// "always" is remembered per TOOL, not for everything.
func TestAlwaysIsRememberedPerTool(t *testing.T) {
	a, _ := scriptedApprover("always\n")
	if ok, _ := a.Approve(context.Background(), callFor("read_file")); !ok {
		t.Fatal("the first call was not allowed")
	}

	if ok, _ := a.Approve(context.Background(), callFor("read_file")); !ok {
		t.Error("the remembered tool was asked about again")
	}
	if ok, _ := a.Approve(context.Background(), callFor("run")); ok {
		t.Error("agreeing to read_file also agreed to run")
	}
}

// A context already over before the gate is even reached must also be a no.
func TestApproverRefusesAnAlreadyCancelledRun(t *testing.T) {
	a, _ := scriptedApprover("y\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ok, err := a.Approve(ctx, callFor("run"))
	if ok {
		t.Error("an already-cancelled run was allowed")
	}
	if err == nil {
		t.Error("no error for a cancelled run")
	}

	if ok, _ := a.Approve(context.Background(), callFor("run")); !ok {
		t.Error("the cancelled run consumed the terminal's answer")
	}
}

// One approver backs every agent.
func TestApproverSerialisesConcurrentPrompts(t *testing.T) {
	const n = 8
	a, out := scriptedApprover(strings.Repeat("y\n", n))
	var wg sync.WaitGroup
	allowed := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := a.Approve(context.Background(), callFor("run"))
			if err != nil {
				t.Errorf("approve: %v", err)
			}
			allowed <- ok
		}()
	}
	wg.Wait()
	close(allowed)
	var yes int
	for ok := range allowed {
		if ok {
			yes++
		}
	}
	if yes != n {
		t.Errorf("%d of %d concurrent approvals were allowed", yes, n)
	}

	if got := strings.Count(out.String(), "allow?"); got != n {
		t.Errorf("%d prompts printed for %d questions", got, n)
	}
}

// The prompt has to be answerable.
func TestDescribeCallIsAnswerable(t *testing.T) {
	req := golm.ToolRequest{Agent: "researcher", Step: 3,
		Call: golm.ToolUse{ID: "1", Name: "run", Input: []byte(`{"argv":["rm","-rf","/"]}`)}}
	got := describeCall(req)
	for _, want := range []string{"run", "rm", "researcher"} {
		if !strings.Contains(got, want) {
			t.Errorf("the prompt does not mention %q:\n%s", want, got)
		}
	}

	huge := golm.ToolRequest{Call: golm.ToolUse{Name: "run",
		Input: []byte(`{"argv":["` + strings.Repeat("x", 100_000) + `"]}`)}}
	shown := describeCall(huge)
	if len(shown) > maxShownInput+512 {
		t.Errorf("a %d-byte prompt scrolls the question away", len(shown))
	}
}
