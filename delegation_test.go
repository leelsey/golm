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

type recallProvider struct {
	mu    sync.Mutex
	seen  []int
	calls int
}

func (r *recallProvider) Name() string               { return "recall" }
func (r *recallProvider) Capabilities() Capabilities { return Capabilities{Tools: true} }

func (r *recallProvider) Complete(_ context.Context, req Request) (Response, error) {
	users := 0
	for _, m := range req.Messages {
		if m.Role == RoleUser {
			users++
		}
	}
	r.mu.Lock()
	r.seen = append(r.seen, users)
	r.calls++
	r.mu.Unlock()
	return Response{Message: AssistantText(fmt.Sprintf("saw %d", users)), StopReason: StopEndTurn}, nil
}

func (r *recallProvider) Stream(ctx context.Context, req Request, fn func(StreamEvent) error) (Response, error) {
	resp, err := r.Complete(ctx, req)
	if err != nil {
		return resp, err
	}
	_ = fn(StreamEvent{Type: EventTextDelta, Text: resp.Message.Text()})
	return resp, nil
}

func (r *recallProvider) history() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int(nil), r.seen...)
}

func twoDelegations() *fakeProvider {
	call := func(id, task string) Response {
		return Response{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: id, Name: "sub", Input: json.RawMessage(`{"task":"` + task + `"}`)},
		}}, StopReason: StopToolUse}
	}
	return &fakeProvider{responses: []Response{
		call("d1", "first"),
		call("d2", "second"),
		{Message: AssistantText("done"), StopReason: StopEndTurn},
	}}
}

// The failure this fixes: the main agent delegates, reads the answer, delegates to the same sub-agent again.
func TestDelegationRemembersWithinAConversation(t *testing.T) {
	sub := &recallProvider{}
	o := NewOrchestrator(nil)
	o.Add("main", "main", &Agent{Provider: twoDelegations(), Model: "m"})
	o.Add("sub", "sub", &Agent{Provider: sub, Model: "s"})
	if err := o.WireDelegation(nil); err != nil {
		t.Fatalf("WireDelegation: %v", err)
	}
	if _, err := o.Run(context.Background(), NewSession(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := sub.history()
	if len(got) != 2 {
		t.Fatalf("sub-agent called %d times, want 2", len(got))
	}
	if got[0] != 1 {
		t.Errorf("first delegation saw %d user turns, want 1", got[0])
	}
	if got[1] != 2 {
		t.Errorf("second delegation saw %d user turns, want 2 — the sub-agent forgot the first", got[1])
	}
}

func TestScopeCallForgetsDeliberately(t *testing.T) {
	sub := &recallProvider{}
	o := NewOrchestrator(nil)
	o.Scope = ScopeCall
	o.Add("main", "main", &Agent{Provider: twoDelegations(), Model: "m"})
	o.Add("sub", "sub", &Agent{Provider: sub, Model: "s"})
	if err := o.WireDelegation(nil); err != nil {
		t.Fatalf("WireDelegation: %v", err)
	}
	if _, err := o.Run(context.Background(), NewSession(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for i, n := range sub.history() {
		if n != 1 {
			t.Errorf("call %d saw %d user turns; ScopeCall must carry nothing over", i, n)
		}
	}
}

// Two conversations must not see each other's sub-agent history, whatever the scope.
func TestConversationsAreIsolated(t *testing.T) {
	sub := &recallProvider{}
	o := NewOrchestrator(nil)
	o.Add("main", "main", &Agent{Provider: twoDelegations(), Model: "m"})
	o.Add("sub", "sub", &Agent{Provider: sub, Model: "s"})
	if err := o.WireDelegation(nil); err != nil {
		t.Fatalf("WireDelegation: %v", err)
	}
	for i := 0; i < 2; i++ {
		main, _ := o.Get("main")
		main.Provider = twoDelegations()
		if _, err := o.Run(context.Background(), NewSession(), "go"); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	got := sub.history()
	if len(got) != 4 {
		t.Fatalf("got %v, want four delegations", got)
	}

	want := []int{1, 2, 1, 2}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("history = %v, want %v — conversations leaked into each other", got, want)
		}
	}
}

// A sub-agent driven directly, outside any conversation, has nothing to belong to and gets a fresh session.
func TestDirectRunHasNoConversation(t *testing.T) {
	o := NewOrchestrator(nil)
	o.Add("sub", "sub", &Agent{Provider: &recallProvider{}, Model: "s"})
	s1, rel1, err := o.sessionFor(context.Background(), "sub")
	if err != nil {
		t.Fatalf("sessionFor: %v", err)
	}
	rel1()
	s2, rel2, err := o.sessionFor(context.Background(), "sub")
	if err != nil {
		t.Fatalf("sessionFor: %v", err)
	}
	rel2()
	if s1 == s2 {
		t.Error("two calls outside a conversation shared a session")
	}
}

func TestSessionsAreReachableAndForgettable(t *testing.T) {
	o := NewOrchestrator(nil)
	o.Add("main", "main", &Agent{Provider: twoDelegations(), Model: "m"})
	o.Add("sub", "sub", &Agent{Provider: &recallProvider{}, Model: "s"})
	if err := o.WireDelegation(nil); err != nil {
		t.Fatalf("WireDelegation: %v", err)
	}
	conv := NewSession()
	if _, err := o.Run(context.Background(), conv, "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	sessions := o.Sessions(conv.ID())
	if len(sessions) != 1 || sessions["sub"] == nil {
		t.Fatalf("Sessions = %v, want the sub-agent's", sessions)
	}
	if sessions["sub"].Len() == 0 {
		t.Error("the sub-agent's session is empty")
	}
	o.Forget(conv.ID())
	if o.Sessions(conv.ID()) != nil {
		t.Error("Forget left the conversation behind")
	}
}

// A long-lived server holds one Orchestrator for as long as it runs.
func TestConversationStoreIsBounded(t *testing.T) {
	o := NewOrchestrator(nil)
	o.MaxConversations = 3
	o.Add("sub", "sub", &Agent{Provider: &recallProvider{}, Model: "s"})
	var ids []string
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("CONV%d", i)
		ids = append(ids, id)
		ctx := withConversation(context.Background(), id)
		_, release, err := o.sessionFor(ctx, "sub")
		if err != nil {
			t.Fatalf("sessionFor: %v", err)
		}
		release()
	}
	o.mu.RLock()
	n := len(o.convs)
	o.mu.RUnlock()
	if n != 3 {
		t.Errorf("kept %d conversations, want the bound of 3", n)
	}
	if o.Sessions(ids[0]) != nil {
		t.Error("the oldest conversation was not evicted")
	}
	if o.Sessions(ids[9]) == nil {
		t.Error("the newest conversation was evicted")
	}
}

// A session carries one run at a time.
func TestSameAgentSerialisesAndOthersDoNot(t *testing.T) {
	var concurrent, peakSame atomic.Int32
	slow := func() Provider {
		return &blockingRecall{onCall: func() {
			n := concurrent.Add(1)
			for {
				old := peakSame.Load()
				if n <= old || peakSame.CompareAndSwap(old, n) {
					break
				}
			}
			time.Sleep(30 * time.Millisecond)
			concurrent.Add(-1)
		}}
	}
	o := NewOrchestrator(nil)
	o.Add("sub", "sub", &Agent{Provider: slow(), Model: "s"})

	ctx := withConversation(context.Background(), "CONV")
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := o.delegate(ctx, "sub", "task"); err != nil {
				t.Errorf("delegate: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := peakSame.Load(); got != 1 {
		t.Errorf("%d runs of one sub-agent overlapped in one conversation; a session carries one run at a time", got)
	}
}

type blockingRecall struct{ onCall func() }

func (b *blockingRecall) Name() string               { return "blocking-recall" }
func (b *blockingRecall) Capabilities() Capabilities { return Capabilities{} }

func (b *blockingRecall) Complete(context.Context, Request) (Response, error) {
	b.onCall()
	return Response{Message: AssistantText("ok"), StopReason: StopEndTurn}, nil
}

func (b *blockingRecall) Stream(ctx context.Context, req Request, _ func(StreamEvent) error) (Response, error) {
	return b.Complete(ctx, req)
}

// A delegation waiting its turn must give up when the run's context ends.
func TestQueuedDelegationHonoursCancellation(t *testing.T) {
	release := make(chan struct{})
	o := NewOrchestrator(nil)
	o.Add("sub", "sub", &Agent{Provider: &blockingRecall{onCall: func() { <-release }}, Model: "s"})

	ctx, cancel := context.WithCancel(withConversation(context.Background(), "CONV"))
	held := make(chan struct{})
	go func() {
		close(held)
		_, _ = o.delegate(ctx, "sub", "first")
	}()
	<-held
	time.Sleep(20 * time.Millisecond)

	done := make(chan error, 1)
	go func() { _, err := o.delegate(ctx, "sub", "second"); done <- err }()
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "context") {
			t.Errorf("queued delegation returned %v, want the cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a queued delegation ignored the cancelled run")
	}
	close(release)
}

// A conversation that survives a process restart has to bring its sub-agents' transcripts with it.
func TestConversationSurvivesSaveAndLoad(t *testing.T) {
	store := newMemStore()
	sub := &recallProvider{}

	first := NewOrchestrator(nil)
	first.Add("main", "main", &Agent{Provider: twoDelegations(), Model: "m"})
	first.Add("sub", "sub", &Agent{Provider: sub, Model: "s"})
	if err := first.WireDelegation(nil); err != nil {
		t.Fatalf("WireDelegation: %v", err)
	}
	conv := NewSession()
	ctx := context.Background()
	if _, err := first.Run(ctx, conv, "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := first.SaveConversation(ctx, store, conv); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	second := NewOrchestrator(nil)
	second.Add("main", "main", &Agent{Provider: &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "d3", Name: "sub", Input: json.RawMessage(`{"task":"third"}`)},
		}}, StopReason: StopToolUse},
		{Message: AssistantText("done"), StopReason: StopEndTurn},
	}}, Model: "m"})
	second.Add("sub", "sub", &Agent{Provider: sub, Model: "s"})
	if err := second.WireDelegation(nil); err != nil {
		t.Fatalf("WireDelegation: %v", err)
	}
	reloaded, err := store.Load(ctx, conv.ID())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	n, err := second.LoadConversation(ctx, store, reloaded)
	if err != nil {
		t.Fatalf("LoadConversation: %v", err)
	}
	if n != 1 {
		t.Fatalf("restored %d sub-agent sessions, want 1", n)
	}
	if _, err := second.Run(ctx, reloaded, "again"); err != nil {
		t.Fatalf("second run: %v", err)
	}
	got := sub.history()
	if len(got) != 3 {
		t.Fatalf("sub-agent history = %v, want three delegations", got)
	}

	if got[2] != 3 {
		t.Errorf("after resuming, the sub-agent saw %d user turns, want 3 — its transcript was lost", got[2])
	}
}

// A session the store no longer has is skipped.
func TestLoadConversationToleratesAPrunedSession(t *testing.T) {
	store := newMemStore()
	o := NewOrchestrator(nil)
	o.Add("sub", "sub", &Agent{Provider: &recallProvider{}, Model: "s"})
	conv := NewSession()
	if err := conv.SetState(subSessionsKey, map[string]string{"sub": "GONEID"}); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	n, err := o.LoadConversation(context.Background(), store, conv)
	if err != nil {
		t.Fatalf("LoadConversation: %v", err)
	}
	if n != 0 {
		t.Errorf("restored %d, want 0", n)
	}
}

func TestSaveConversationIsANoOpWithoutAStore(t *testing.T) {
	o := NewOrchestrator(nil)
	if err := o.SaveConversation(context.Background(), nil, NewSession()); err != nil {
		t.Errorf("SaveConversation with no store: %v", err)
	}
	if n, err := o.LoadConversation(context.Background(), nil, NewSession()); err != nil || n != 0 {
		t.Errorf("LoadConversation with no store: %d, %v", n, err)
	}
}

// Restore must leave a slot with a delegation IN FLIGHT alone.
func TestRestoreDoesNotReplaceABusySlot(t *testing.T) {
	inSub := make(chan struct{})
	release := make(chan struct{})
	sub := &Agent{Model: "s", Provider: &blockingProvider{entered: inSub, release: release}}
	o := NewOrchestrator(nil)
	o.Add("main", "main", &Agent{Model: "m", Provider: &fakeProvider{responses: []Response{
		{Message: AssistantText("ok"), StopReason: StopEndTurn},
	}}})
	o.Add("worker", "sub", sub)

	main := NewSession()
	ctx := withConversation(context.Background(), main.ID())
	done := make(chan struct{})
	go func() { defer close(done); _, _ = o.delegate(ctx, "worker", "task") }()
	<-inSub

	replacement := NewSession()
	o.Restore(main.ID(), map[string]*Session{"worker": replacement})
	if got := o.Sessions(main.ID())["worker"]; got == replacement {
		t.Error("Restore replaced a slot with a delegation in flight")
	}

	close(release)
	<-done

	o.Restore(main.ID(), map[string]*Session{"worker": replacement})
	if got := o.Sessions(main.ID())["worker"]; got != replacement {
		t.Error("Restore did not replace an idle slot")
	}
}

type blockingProvider struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingProvider) Name() string               { return "blocking" }
func (b *blockingProvider) Capabilities() Capabilities { return Capabilities{Tools: true} }
func (b *blockingProvider) Complete(ctx context.Context, _ Request) (Response, error) {
	b.once.Do(func() { close(b.entered) })
	select {
	case <-b.release:
	case <-ctx.Done():
		return Response{}, ctx.Err()
	}
	return Response{Message: AssistantText("sub done"), StopReason: StopEndTurn}, nil
}
func (b *blockingProvider) Stream(ctx context.Context, r Request, _ func(StreamEvent) error) (Response, error) {
	return b.Complete(ctx, r)
}
