// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// Audit 1: does a delegation inherit the PARENT's tool-call id?
func TestNestedToolCallIDIsTheInnerOne(t *testing.T) {
	var innerID string
	inner := NewRegistry()
	inner.Register(NewTool("probe", "d", json.RawMessage(`{"type":"object"}`),
		func(ctx context.Context, _ json.RawMessage) (string, error) {
			innerID = ToolCallIDOf(ctx)
			return "ok", nil
		}))
	subProv := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "INNER", Name: "probe", Input: json.RawMessage(`{}`)},
		}}, StopReason: StopToolUse},
		{Message: AssistantText("sub done"), StopReason: StopEndTurn},
	}}
	mainProv := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "OUTER", Name: "sub", Input: json.RawMessage(`{"task":"go"}`)},
		}}, StopReason: StopToolUse},
		{Message: AssistantText("done"), StopReason: StopEndTurn},
	}}
	o := NewOrchestrator(nil)
	o.Add("main", "main", &Agent{Provider: mainProv, Model: "m"})
	o.Add("sub", "sub", &Agent{Provider: subProv, Model: "s", Tools: inner})
	if err := o.WireDelegation(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Run(context.Background(), NewSession(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if innerID != "INNER" {
		t.Errorf("the nested tool saw call id %q, want INNER — it is reporting under the delegation's id", innerID)
	}
}

// Audit 2: does a cancelled compaction lose the transcript?
func TestFailedCompactionLeavesTheTranscriptIntact(t *testing.T) {
	s := NewSession()
	for i := 0; i < 10; i++ {
		s.Append(UserText("q"), AssistantText("a"))
	}
	before := s.Len()
	_, err := s.Compact(context.Background(), CompactConfig{
		KeepLast: 4,
		Summarise: func(context.Context, []Message) (Summary, error) {
			return Summary{}, errors.New("summariser unavailable")
		},
	})
	if err == nil {
		t.Fatal("a failing summariser reported success")
	}
	if s.Len() != before {
		t.Errorf("the transcript went from %d to %d messages on a FAILED compaction", before, s.Len())
	}
}

// Audit 3: is Budget.Spend's overshoot bounded to one call?
func TestBudgetOvershootIsOneCall(t *testing.T) {
	reg := NewRegistry()
	reg.Register(echoTool())
	p := &costly{
		resp: Response{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "t", Name: "echo", Input: json.RawMessage(`{}`)},
		}}, StopReason: StopToolUse},
		per: Usage{InputTokens: 100, OutputTokens: 100},
	}
	b := NewBudget(150)
	a := &Agent{Provider: p, Model: "m", Tools: reg, Budget: b, MaxSteps: 20}
	_, err := a.Run(context.Background(), NewSession(), "go")
	if !errors.Is(err, ErrTokenBudget) {
		t.Fatalf("err = %v", err)
	}

	if b.Spent() != 200 {
		t.Errorf("spent %d; the ceiling should be crossed by at most one call", b.Spent())
	}
}

// Audit 4: does Trim keep the transcript provider-valid?
func TestTrimNeverLeavesANonUserFirstMessage(t *testing.T) {
	for keep := 1; keep <= 12; keep++ {
		s := NewSession()
		for i := 0; i < 5; i++ {
			s.Append(UserText("q"),
				Message{Role: RoleAssistant, Content: []Content{ToolUse{ID: "t", Name: "x"}}},
				Message{Role: RoleTool, Content: []Content{ToolResult{ToolUseID: "t", Name: "x"}}},
				AssistantText("a"))
		}
		s.Trim(keep)
		h := s.History()
		if len(h) == 0 {
			continue
		}
		if h[0].Role != RoleUser {
			t.Errorf("keepLast=%d left a %s message first; providers reject that", keep, h[0].Role)
		}
	}
}

// Audit 5: concurrent runs on ONE agent, many sessions
func TestOneAgentManySessionsConcurrently(t *testing.T) {
	reg := NewRegistry()
	reg.Register(echoTool())
	a := &Agent{Provider: &alwaysProvider{}, Model: "m", Tools: reg, Budget: NewBudget(0)}
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.Run(context.Background(), NewSession(), "go"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent run: %v", err)
	}
}

type alwaysProvider struct{}

func (alwaysProvider) Name() string               { return "always" }
func (alwaysProvider) Capabilities() Capabilities { return Capabilities{Tools: true} }
func (alwaysProvider) Complete(context.Context, Request) (Response, error) {
	return Response{Message: AssistantText("ok"), StopReason: StopEndTurn,
		Usage: Usage{InputTokens: 1, OutputTokens: 1}}, nil
}
func (a alwaysProvider) Stream(ctx context.Context, r Request, fn func(StreamEvent) error) (Response, error) {
	return a.Complete(ctx, r)
}

// Audit 6: does a tool that ignores ctx hold the run past its deadline?
func TestRunReturnsEvenWhenAToolIgnoresItsContext(t *testing.T) {
	reg := NewRegistry()
	release := make(chan struct{})
	reg.Register(NewTool("stuck", "d", json.RawMessage(`{"type":"object"}`),
		func(context.Context, json.RawMessage) (string, error) {
			<-release
			return "late", nil
		}))
	fp := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "t1", Name: "stuck", Input: json.RawMessage(`{}`)},
		}}, StopReason: StopToolUse},
		{Message: AssistantText("moved on"), StopReason: StopEndTurn},
	}}
	a := &Agent{Provider: fp, Model: "m", Tools: reg, ToolTimeout: 50 * time.Millisecond}
	done := make(chan struct{})
	go func() { defer close(done); _, _ = a.Run(context.Background(), NewSession(), "go") }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a tool that ignores its context held the run")
	}
	close(release)
}

// Audit 7: is a tool result truncated on the ERROR path too?
func TestToolErrorIsBoundedLikeASuccess(t *testing.T) {
	reg := NewRegistry()
	reg.Register(NewTool("loud", "d", json.RawMessage(`{"type":"object"}`),
		func(context.Context, json.RawMessage) (string, error) {
			return "", errors.New(strings.Repeat("x", 10000))
		}))
	fp := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "t1", Name: "loud", Input: json.RawMessage(`{}`)},
		}}, StopReason: StopToolUse},
		{Message: AssistantText("ok"), StopReason: StopEndTurn},
	}}
	sess := NewSession()
	a := &Agent{Provider: fp, Model: "m", Tools: reg, MaxToolResultBytes: 100}
	if _, err := a.Run(context.Background(), sess, "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, m := range sess.History() {
		for _, c := range m.Content {
			if tr, ok := c.(ToolResult); ok && len(tr.Text()) > 200 {
				t.Errorf("an error result is %d bytes; it is re-sent on every later step too", len(tr.Text()))
			}
		}
	}
}

// Audit: is sessionstore.Memory a real copy, or does it hand back the live session?
func TestSnapshotIsDetachedFromTheLiveSession(t *testing.T) {
	s := NewSession()
	s.Append(UserText("original"))
	d := s.Snapshot()
	s.Append(UserText("added after the snapshot"))
	if len(d.History) != 1 {
		t.Errorf("the snapshot grew with the session: %d messages", len(d.History))
	}

	rebuilt := d.Session()
	rebuilt.Append(UserText("added to the copy"))
	if s.Len() != 2 {
		t.Errorf("writing to a rebuilt session reached the original: %d messages", s.Len())
	}
}

// Audit: does Append copy the byte payloads
func TestAppendCopiesInnerPayloads(t *testing.T) {
	buf := []byte("original bytes")
	s := NewSession()
	s.Append(Message{Role: RoleUser, Content: []Content{Image{Data: buf, MediaType: "image/png"}}})
	copy(buf, "OVERWRITTEN!!!")
	img, ok := s.History()[0].Content[0].(Image)
	if !ok {
		t.Fatalf("content = %T", s.History()[0].Content[0])
	}
	if string(img.Data) != "original bytes" {
		t.Errorf("the session holds %q; a caller reusing its buffer rewrote history", img.Data)
	}
}

// Audit: two agents sharing one Registry, called concurrently
func TestRegistryIsSafeForConcurrentUse(t *testing.T) {
	reg := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			reg.Register(NewTool("t"+string(rune('a'+i)), "d", json.RawMessage(`{"type":"object"}`),
				func(context.Context, json.RawMessage) (string, error) { return "ok", nil }))
		}(i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = reg.Defs()
			_ = reg.List()
			_, _ = reg.Get("ta")
		}()
	}
	wg.Wait()
	if len(reg.List()) != 8 {
		t.Errorf("%d tools registered, want 8", len(reg.List()))
	}
}

// Audit: does the fan-out's own concurrency bound also bound the BUDGET check
func TestFanStopsOnceTheBudgetIsGone(t *testing.T) {
	o := NewOrchestrator(nil)
	o.FanParallel = 1
	o.Budget = NewBudget(120)
	o.Add("worker", "sub", &Agent{Provider: &costly{
		resp: Response{Message: AssistantText("done"), StopReason: StopEndTurn},
		per:  Usage{InputTokens: 50, OutputTokens: 50},
	}, Model: "w"})

	results, err := o.Fan(context.Background(), "worker", []string{"a", "b", "c", "d"})
	if err != nil {
		t.Fatalf("Fan: %v", err)
	}
	var ok, failed int
	for _, r := range results {
		if r.Err != nil {
			failed++
		} else {
			ok++
		}
	}
	if failed == 0 {
		t.Errorf("all %d branches ran against a 120-token ceiling at 100 each", ok)
	}

	if spent := o.Budget.Spent(); spent > 200 {
		t.Errorf("spent %d against a ceiling of 120", spent)
	}
}

// Audit: a Router that panics must not take the run down
func TestPanickingRouterIsContained(t *testing.T) {
	o := NewOrchestrator(nil)
	o.Add("main", "main", &Agent{Provider: &fakeProvider{responses: []Response{
		{Message: AssistantText("ok"), StopReason: StopEndTurn},
	}}, Model: "m"})
	o.Router = func(context.Context, RouteInput) (Route, error) { panic("router exploded") }

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("a panicking router took the run down: %v", r)
		}
	}()
	if _, err := o.Run(context.Background(), NewSession(), "go"); err == nil {
		t.Error("a panicking router reported success")
	} else if !strings.Contains(err.Error(), "panic") {
		t.Errorf("err = %v, should name the panic", err)
	}
}

// A callback runs inside a defer.
func TestPanickingOnToolResultIsContained(t *testing.T) {
	reg := NewRegistry()
	reg.Register(echoTool())
	fp := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "t1", Name: "echo", Input: json.RawMessage(`{}`)},
		}}, StopReason: StopToolUse},
		{Message: AssistantText("done"), StopReason: StopEndTurn},
	}}
	a := &Agent{Provider: fp, Model: "m", Tools: reg,
		OnToolResult: func(ToolResult) { panic("callback exploded") }}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("a panicking callback took the run down: %v", r)
		}
	}()
	res, err := a.Run(context.Background(), NewSession(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Text() != "done" {
		t.Errorf("the run did not complete: %q", res.Text())
	}
}

// Audit: a Summariser that panics during automatic compaction
func TestPanickingSummariserIsContained(t *testing.T) {
	s := NewSession()
	for i := 0; i < 10; i++ {
		s.Append(UserText("q"), AssistantText("a"))
	}
	before := s.Len()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("a panicking summariser took the caller down: %v", r)
		}
	}()
	_, err := s.Compact(context.Background(), CompactConfig{
		KeepLast:  4,
		Summarise: func(context.Context, []Message) (Summary, error) { panic("summariser exploded") },
	})
	if err == nil {
		t.Error("a panicking summariser reported success")
	}
	if s.Len() != before {
		t.Errorf("the transcript changed: %d -> %d", before, s.Len())
	}
}

// Audit: an Archive that panics or fails must not lose the transcript
func TestFailingArchiveDoesNotLoseTheConversation(t *testing.T) {
	s := NewSession()
	for i := 0; i < 10; i++ {
		s.Append(UserText("q"), AssistantText("a"))
	}
	before := s.Len()
	_, err := s.Compact(context.Background(), CompactConfig{
		KeepLast:  4,
		Summarise: func(context.Context, []Message) (Summary, error) { return Summary{Text: "a summary"}, nil },
		Archive:   func(context.Context, *Session) error { return errors.New("the store is gone") },
	})
	if err == nil {
		t.Fatal("a failing archive reported success")
	}
	if s.Len() != before {
		t.Errorf("the head was dropped though the archive failed: %d -> %d messages", before, s.Len())
	}
}

// Audit: a stream callback that returns an error must stop the run rather than be ignored
func TestStreamCallbackErrorStopsTheRun(t *testing.T) {
	a := &Agent{Provider: &chatty{}, Model: "m"}
	sentinel := errors.New("the caller has had enough")
	_, err := a.Stream(context.Background(), NewSession(), "go", func(StreamEvent) error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want the caller's own error", err)
	}
}

type chatty struct{}

func (chatty) Name() string               { return "chatty" }
func (chatty) Capabilities() Capabilities { return Capabilities{Streaming: true} }
func (chatty) Complete(context.Context, Request) (Response, error) {
	return Response{Message: AssistantText("a b c"), StopReason: StopEndTurn}, nil
}
func (c chatty) Stream(ctx context.Context, r Request, fn func(StreamEvent) error) (Response, error) {
	for _, w := range []string{"a ", "b ", "c"} {
		if err := fn(StreamEvent{Type: EventTextDelta, Text: w}); err != nil {
			return Response{}, err
		}
	}
	return c.Complete(ctx, r)
}

// Audit: the conversation store's eviction must not drop a conversation whose sub-agent is mid-run
func TestEvictionSkipsAConversationInFlight(t *testing.T) {
	release := make(chan struct{})
	o := NewOrchestrator(nil)
	o.MaxConversations = 2
	o.Add("sub", "sub", &Agent{Provider: &blockingRecall{onCall: func() { <-release }}, Model: "s"})

	busy := withConversation(context.Background(), "BUSY")
	started := make(chan struct{})
	go func() {
		close(started)
		_, _ = o.delegate(busy, "sub", "hold the slot")
	}()
	<-started
	time.Sleep(50 * time.Millisecond)

	for i := 0; i < 8; i++ {
		ctx := withConversation(context.Background(), "OTHER"+string(rune('A'+i)))
		_, rel, err := o.sessionFor(ctx, "sub")
		if err != nil {
			t.Fatalf("sessionFor: %v", err)
		}
		rel()
	}
	if o.Sessions("BUSY") == nil {
		t.Error("a conversation with a run in flight was evicted out from under it")
	}
	close(release)
}

// Audit: two agents registered under one name
func TestAddReplacesRatherThanDuplicating(t *testing.T) {
	o := NewOrchestrator(nil)
	first := &Agent{Provider: &fakeProvider{}, Model: "one"}
	second := &Agent{Provider: &fakeProvider{}, Model: "two"}
	o.Add("x", "sub", first)
	o.Add("x", "sub", second)
	got, ok := o.Get("x")
	if !ok || got != second {
		t.Error("re-adding a name did not replace the agent")
	}
	if n := len(o.Agents()); n != 1 {
		t.Errorf("%d agents listed for one name", n)
	}
}

// Audit: a tool whose result is only media, with a byte cap
func TestMediaOnlyResultIsBounded(t *testing.T) {
	reg := NewRegistry()
	reg.Register(NewContentTool("shot", "d", json.RawMessage(`{"type":"object"}`),
		func(context.Context, json.RawMessage) ([]ToolContent, error) {
			return []ToolContent{Image{Data: make([]byte, 50000), MediaType: "image/png"}}, nil
		}))
	fp := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "t1", Name: "shot", Input: json.RawMessage(`{}`)},
		}}, StopReason: StopToolUse},
		{Message: AssistantText("ok"), StopReason: StopEndTurn},
	}}
	sess := NewSession()
	a := &Agent{Provider: fp, Model: "m", Tools: reg, MaxToolResultBytes: 1000}
	if _, err := a.Run(context.Background(), sess, "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, m := range sess.History() {
		for _, c := range m.Content {
			tr, ok := c.(ToolResult)
			if !ok {
				continue
			}
			for _, inner := range tr.Content {
				if img, ok := inner.(Image); ok && len(img.Data) > 1000 {
					t.Errorf("a %d-byte image survived a 1000-byte cap", len(img.Data))
				}
			}

			if !strings.Contains(tr.Text(), "omitted") {
				t.Errorf("the oversized image was not reported as omitted: %q", tr.Text())
			}
		}
	}
}
