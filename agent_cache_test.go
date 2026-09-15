// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"fmt"
	"testing"
)

type recordingProvider struct {
	responses []Response
	calls     int
	reqs      []Request
}

func (p *recordingProvider) Name() string               { return "recording" }
func (p *recordingProvider) Capabilities() Capabilities { return Capabilities{Tools: true} }

func (p *recordingProvider) Complete(_ context.Context, req Request) (Response, error) {
	p.reqs = append(p.reqs, req)
	r := p.responses[p.calls]
	p.calls++
	return r, nil
}

func (p *recordingProvider) Stream(ctx context.Context, req Request, fn func(StreamEvent) error) (Response, error) {
	r, err := p.Complete(ctx, req)
	if err != nil {
		return Response{}, err
	}
	_ = fn(StreamEvent{Type: EventDone})
	return r, nil
}

func TestAgentCachePromptOffByDefault(t *testing.T) {
	p := &recordingProvider{responses: []Response{{Message: AssistantText("done")}}}
	a := &Agent{Provider: p, Model: "m", System: "sys"}
	if _, err := a.Run(context.Background(), NewSession(), "go"); err != nil {
		t.Fatal(err)
	}
	if got := p.reqs[0].Cache; got != (CacheConfig{}) {
		t.Errorf("Cache = %#v, want the zero value when CachePrompt is unset", got)
	}
}

func TestAgentCachePromptCoversSystemAndWholeTranscript(t *testing.T) {
	p := &recordingProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "t1", Name: "echo", Input: []byte(`{}`)},
		}}, StopReason: StopToolUse},
		{Message: AssistantText("done")},
	}}
	tools := NewRegistry()
	tools.Register(echoTool())
	a := &Agent{Provider: p, Model: "m", System: "sys", Tools: tools, CachePrompt: true}

	if _, err := a.Run(context.Background(), NewSession(), "go"); err != nil {
		t.Fatal(err)
	}
	if len(p.reqs) != 2 {
		t.Fatalf("recorded %d requests, want 2", len(p.reqs))
	}
	for i, req := range p.reqs {
		if !req.System.Cached() {
			t.Errorf("step %d did not ask for the system prompt to be cached", i)
		}

		if got, want := req.Cache.MessagePrefix, len(req.Messages); got != want {
			t.Errorf("step %d: MessagePrefix = %d, want %d", i, got, want)
		}
	}

	if len(p.reqs[1].Messages) <= len(p.reqs[0].Messages) {
		t.Errorf("transcript did not grow between steps: %d then %d",
			len(p.reqs[0].Messages), len(p.reqs[1].Messages))
	}

	for i, m := range p.reqs[0].Messages {
		if p.reqs[1].Messages[i].Text() != m.Text() {
			t.Errorf("message %d changed between steps; the prefix is not stable", i)
		}
	}
}

// A caller may assemble a tiered prompt and still leave CachePrompt off.
func TestAgentCachePromptStripsMarksWhenOff(t *testing.T) {
	p := &recordingProvider{responses: []Response{{Message: AssistantText("done")}}}
	sys := SystemPrompt{}.Add("identity").Break().Add("memory")
	a := &Agent{Provider: p, Model: "m", SystemPrompt: sys}
	if _, err := a.Run(context.Background(), NewSession(), "go"); err != nil {
		t.Fatal(err)
	}
	if p.reqs[0].System.Cached() {
		t.Error("a breakpoint reached the wire with CachePrompt off")
	}

	if got, want := p.reqs[0].System.Text(), sys.Text(); got != want {
		t.Errorf("System = %q, want %q", got, want)
	}

	if !sys.Cached() {
		t.Error("the caller's SystemPrompt lost its breakpoint")
	}
}

// System is the older field and keeps its meaning.
func TestAgentSystemLeadsTheAssembledPrompt(t *testing.T) {
	p := &recordingProvider{responses: []Response{{Message: AssistantText("done")}}}
	a := &Agent{Provider: p, Model: "m", System: "lead",
		SystemPrompt: SystemPrompt{}.Add("identity", "memory")}
	if _, err := a.Run(context.Background(), NewSession(), "go"); err != nil {
		t.Fatal(err)
	}
	secs := p.reqs[0].System.Sections()
	want := []string{"lead", "identity", "memory"}
	if len(secs) != len(want) {
		t.Fatalf("sent %d sections, want %d: %#v", len(secs), len(want), secs)
	}
	for i, w := range want {
		if secs[i].Text != w {
			t.Errorf("section %d = %q, want %q", i, secs[i].Text, w)
		}
	}
}

// A provider has very few breakpoints and a span below the minimum cacheable size is ignored in silence.
func TestAgentSystemIsUnmarkedInFrontOfTiers(t *testing.T) {
	p := &recordingProvider{responses: []Response{{Message: AssistantText("done")}}}
	a := &Agent{Provider: p, Model: "m", System: "lead", CachePrompt: true,
		SystemPrompt: SystemPrompt{}.Add("identity").Break().Add("memory")}
	if _, err := a.Run(context.Background(), NewSession(), "go"); err != nil {
		t.Fatal(err)
	}
	secs := p.reqs[0].System.Sections()
	if len(secs) != 3 || secs[0].Text != "lead" {
		t.Fatalf("System did not lead: %#v", secs)
	}
	if secs[0].Cache {
		t.Error("the System lead spent a breakpoint on a span below the minimum cacheable size")
	}
	if !secs[1].Cache {
		t.Error("the tier breakpoint was lost")
	}
	if secs[2].Cache {
		t.Error("a breakpoint at the end of the prompt caches the volatile tier too")
	}
}

func TestAgentCachePromptTTLIsForwarded(t *testing.T) {
	p := &recordingProvider{responses: []Response{{Message: AssistantText("done")}}}
	a := &Agent{Provider: p, Model: "m", System: "sys", CachePrompt: true, CachePromptTTL: CacheTTL1h}
	if _, err := a.Run(context.Background(), NewSession(), "go"); err != nil {
		t.Fatal(err)
	}
	if got := p.reqs[0].Cache.TTL; got != CacheTTL1h {
		t.Errorf("TTL = %q, want %q", got, CacheTTL1h)
	}
}

// KeepLast and CachePrompt pull against each other across runs.
func TestTrimMovesTheCachePrefixBetweenRuns(t *testing.T) {
	ok := Response{Message: AssistantText("ok"), StopReason: StopEndTurn}
	rp := &recordingProvider{responses: []Response{ok, ok, ok, ok}}
	sess := NewSession()
	a := &Agent{
		Provider: rp, Model: "x",
		CachePrompt: true,
		KeepLast:    2,
	}

	for run := 0; run < 4; run++ {
		if _, err := a.Run(context.Background(), sess, fmt.Sprintf("question-%d", run)); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
	}
	if len(rp.reqs) != 4 {
		t.Fatalf("want 4 requests, got %d", len(rp.reqs))
	}

	for i, r := range rp.reqs {
		if r.Cache.MessagePrefix != len(r.Messages) {
			t.Errorf("run %d: MessagePrefix = %d, want %d (the whole transcript)",
				i, r.Cache.MessagePrefix, len(r.Messages))
		}
	}

	first, last := rp.reqs[0].Messages[0].Text(), rp.reqs[3].Messages[0].Text()
	if first == last {
		t.Errorf("both runs still start at %q — if Trim has stopped moving the prefix, "+
			"KeepLast and CachePrompt no longer conflict and the field docs should say so", first)
	}

	if len(rp.reqs[3].Messages) > 4 {
		t.Errorf("run 4 sent %d messages; KeepLast=2 should hold it near the window",
			len(rp.reqs[3].Messages))
	}
}

// An empty tier is still a tier the caller wrote.
func TestAgentSystemKeepsItsMarkWhenEveryTierIsEmpty(t *testing.T) {
	solo := (&Agent{System: "sys", CachePrompt: true}).systemPrompt()
	withEmptyTier := (&Agent{
		System:       "sys",
		SystemPrompt: SystemPrompt{{Text: ""}},
		CachePrompt:  true,
	}).systemPrompt()

	if solo.Text() != withEmptyTier.Text() {
		t.Fatalf("text differs: %q vs %q", solo.Text(), withEmptyTier.Text())
	}
	if !withEmptyTier.Cached() {
		t.Errorf("same prompt as %q, which caches, but this one does not", solo.Text())
	}
}
