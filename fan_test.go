// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type echoTask struct {
	mu      sync.Mutex
	seen    []string
	live    atomic.Int32
	peak    atomic.Int32
	hold    time.Duration
	failOn  string
	panicOn string
}

func (e *echoTask) Name() string               { return "echo-task" }
func (e *echoTask) Capabilities() Capabilities { return Capabilities{} }

func (e *echoTask) Complete(ctx context.Context, req Request) (Response, error) {
	n := e.live.Add(1)
	for {
		old := e.peak.Load()
		if n <= old || e.peak.CompareAndSwap(old, n) {
			break
		}
	}
	defer e.live.Add(-1)

	var task string
	users := 0
	for _, m := range req.Messages {
		if m.Role == RoleUser {
			users++
			task = m.Text()
		}
	}
	e.mu.Lock()
	e.seen = append(e.seen, task)
	e.mu.Unlock()
	if users != 1 {
		return Response{}, fmt.Errorf("fan-out branch saw %d user turns; each task must be on its own", users)
	}
	if e.hold > 0 {
		select {
		case <-time.After(e.hold):
		case <-ctx.Done():
			return Response{}, ctx.Err()
		}
	}
	if e.panicOn != "" && task == e.panicOn {
		panic("task exploded")
	}
	if e.failOn != "" && task == e.failOn {
		return Response{}, fmt.Errorf("refused %q", task)
	}
	return Response{Message: AssistantText("did " + task), StopReason: StopEndTurn,
		Usage: Usage{InputTokens: 2, OutputTokens: 3}}, nil
}

func (e *echoTask) Stream(ctx context.Context, req Request, _ func(StreamEvent) error) (Response, error) {
	return e.Complete(ctx, req)
}

func fanOrchestrator(p Provider) *Orchestrator {
	o := NewOrchestrator(nil)
	o.Add("worker", "sub", &Agent{Provider: p, Model: "w"})
	return o
}

func TestFanRunsEveryTaskAndKeepsInputOrder(t *testing.T) {
	p := &echoTask{hold: 20 * time.Millisecond}
	o := fanOrchestrator(p)
	o.FanParallel = 4

	tasks := []string{"alpha", "beta", "gamma", "delta"}
	start := time.Now()
	results, err := o.Fan(context.Background(), "worker", tasks)
	if err != nil {
		t.Fatalf("Fan: %v", err)
	}
	if len(results) != len(tasks) {
		t.Fatalf("got %d results for %d tasks", len(results), len(tasks))
	}

	for i, r := range results {
		if r.Index != i || r.Task != tasks[i] {
			t.Errorf("result %d = %+v, want task %q", i, r, tasks[i])
		}
		if r.Err != nil || r.Text != "did "+tasks[i] {
			t.Errorf("result %d: %q / %v", i, r.Text, r.Err)
		}
	}
	if peak := p.peak.Load(); peak < 2 {
		t.Errorf("peak concurrency %d — the fan-out ran serially", peak)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("four 20ms tasks took %v; they did not overlap", elapsed)
	}
}

func TestFanRespectsItsConcurrencyBound(t *testing.T) {
	p := &echoTask{hold: 20 * time.Millisecond}
	o := fanOrchestrator(p)
	o.FanParallel = 2
	if _, err := o.Fan(context.Background(), "worker", []string{"a", "b", "c", "d", "e", "f"}); err != nil {
		t.Fatalf("Fan: %v", err)
	}
	if peak := p.peak.Load(); peak > 2 {
		t.Errorf("%d tasks ran at once, over the bound of 2", peak)
	}
}

// The useful answer to "summarise these twelve files" is eleven summaries and a note, not an error.
func TestFanReportsOneFailureWithoutLosingTheRest(t *testing.T) {
	p := &echoTask{failOn: "bad"}
	o := fanOrchestrator(p)
	results, err := o.Fan(context.Background(), "worker", []string{"good", "bad", "also good"})
	if err != nil {
		t.Fatalf("Fan: %v — one failing task must not fail the fan-out", err)
	}
	if results[0].Err != nil || results[2].Err != nil {
		t.Errorf("healthy tasks were affected: %v / %v", results[0].Err, results[2].Err)
	}
	if results[1].Err == nil {
		t.Error("the failing task reported success")
	}

	out := formatFan(results)
	if !strings.Contains(out, "FAILED") || !strings.Contains(out, "1 of 3") {
		t.Errorf("rendered output hides the failure:\n%s", out)
	}
	if !strings.Contains(out, "did good") || !strings.Contains(out, "did also good") {
		t.Errorf("rendered output lost a good answer:\n%s", out)
	}
}

func TestFanSurvivesAPanickingTask(t *testing.T) {
	p := &echoTask{panicOn: "boom"}
	o := fanOrchestrator(p)
	results, err := o.Fan(context.Background(), "worker", []string{"fine", "boom"})
	if err != nil {
		t.Fatalf("Fan: %v", err)
	}
	if results[0].Err != nil {
		t.Errorf("the healthy task failed: %v", results[0].Err)
	}
	if results[1].Err == nil || !strings.Contains(results[1].Err.Error(), "panicked") {
		t.Errorf("panic was not contained: %v", results[1].Err)
	}
}

func TestFanBoundsTheNumberOfTasks(t *testing.T) {
	o := fanOrchestrator(&echoTask{})
	o.MaxFan = 3
	_, err := o.Fan(context.Background(), "worker", []string{"a", "b", "c", "d"})
	if err == nil || !strings.Contains(err.Error(), "limit of 3") {
		t.Errorf("err = %v, want a refusal naming the limit", err)
	}
	if _, err := o.Fan(context.Background(), "worker", nil); err == nil {
		t.Error("an empty fan-out was accepted")
	}
	if _, err := o.Fan(context.Background(), "nobody", []string{"a"}); err == nil {
		t.Error("a fan-out to an unknown agent was accepted")
	}
}

// Fan-out branches are independent by definition.
func TestFanBranchesNeverShareASession(t *testing.T) {
	p := &echoTask{}
	o := fanOrchestrator(p)
	o.Scope = ScopeConversation
	ctx := withConversation(context.Background(), "CONV")
	if _, err := o.Fan(ctx, "worker", []string{"one", "two", "three"}); err != nil {
		t.Fatalf("Fan: %v", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.seen) != 3 {
		t.Fatalf("saw %v", p.seen)
	}
}

func TestFanUsageReachesTheOrchestrator(t *testing.T) {
	o := fanOrchestrator(&echoTask{})
	before := o.Usage().Total()
	if _, err := o.Fan(context.Background(), "worker", []string{"a", "b"}); err != nil {
		t.Fatalf("Fan: %v", err)
	}
	if got := o.Usage().Total() - before; got != 10 {
		t.Errorf("fan-out booked %d tokens, want 10 (two calls at 5)", got)
	}
}

func TestFanToolSplitsAListForTheModel(t *testing.T) {
	p := &echoTask{}
	o := fanOrchestrator(p)
	tool := o.AsFanTool("worker", "")
	if tool.Name() != "worker_each" {
		t.Errorf("tool name = %q", tool.Name())
	}
	if !strings.Contains(tool.Description(), "INDEPENDENT") {
		t.Errorf("the contract must be stated, not implied: %q", tool.Description())
	}
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"tasks":["read a","read b"]}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var text strings.Builder
	for _, c := range out {
		if t2, ok := c.(Text); ok {
			text.WriteString(t2.Text)
		}
	}
	for _, want := range []string{"did read a", "did read b", "## 1.", "## 2."} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("output missing %q:\n%s", want, text.String())
		}
	}
}

func TestFanHonoursACancelledRun(t *testing.T) {
	p := &echoTask{hold: time.Second}
	o := fanOrchestrator(p)
	o.FanParallel = 1
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	results, err := o.Fan(ctx, "worker", []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("Fan: %v", err)
	}
	var failed int
	for _, r := range results {
		if r.Err != nil {
			failed++
		}
	}
	if failed == 0 {
		t.Error("a cancelled fan-out reported every task as a success")
	}
}

func TestFanIsBoundedByDelegationDepth(t *testing.T) {
	o := fanOrchestrator(&echoTask{})
	o.MaxDepth = 1
	ctx := context.WithValue(context.Background(), delegationDepthKey{}, 1)
	if _, err := o.Fan(ctx, "worker", []string{"a"}); err == nil {
		t.Error("fan-out ignored the delegation depth limit")
	}
}
