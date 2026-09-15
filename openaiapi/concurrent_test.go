// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package openaiapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/openaiapi"
	"github.com/leelsey/golm/sessionstore"
)

// Two requests naming the SAME golm_session must not each load a private copy of the conversation.
func TestConcurrentRequestsOnOneSessionDoNotLoseTurns(t *testing.T) {
	p := &echoProvider{}
	s := openaiapi.NewServer()
	s.Add("assistant", &golm.Agent{Provider: p, Model: "m"}, "")
	s.Store = sessionstore.NewFiles(t.TempDir())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	const n = 6
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp := post(t, ts, "/v1/chat/completions", map[string]any{
				"model": "assistant", "golm_session": "SHARED",
				"messages": []map[string]any{user(fmt.Sprintf("turn %d", i))},
			}, "")
			defer resp.Body.Close()
			var out map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&out)
		}(i)
	}
	wg.Wait()

	sess, err := s.Store.Load(context.Background(), "SHARED")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var users int
	for _, m := range sess.History() {
		if m.Role == golm.RoleUser {
			users++
		}
	}
	if users != n {
		t.Errorf("the conversation kept %d of %d turns; concurrent requests overwrote each other", users, n)
	}
}

// Different sessions must still run at once.
func TestDifferentSessionsStillRunConcurrently(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	p := &gateProvider{entered: entered, release: release}
	s := openaiapi.NewServer()
	s.Add("assistant", &golm.Agent{Provider: p, Model: "m"}, "")
	s.Store = sessionstore.NewFiles(t.TempDir())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	for _, name := range []string{"A", "B"} {
		go func(name string) {
			resp := post(t, ts, "/v1/chat/completions", map[string]any{
				"model": "assistant", "golm_session": name,
				"messages": []map[string]any{user("hi")},
			}, "")
			resp.Body.Close()
		}(name)
	}

	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-timeoutAfter():
			t.Fatal("two different sessions were serialised against each other")
		}
	}
	close(release)
}

type gateProvider struct {
	entered chan struct{}
	release chan struct{}
}

func (g *gateProvider) Name() string                    { return "gate" }
func (g *gateProvider) Capabilities() golm.Capabilities { return golm.Capabilities{Streaming: true} }
func (g *gateProvider) Complete(ctx context.Context, _ golm.Request) (golm.Response, error) {
	g.entered <- struct{}{}
	select {
	case <-g.release:
	case <-ctx.Done():
		return golm.Response{}, ctx.Err()
	}
	return golm.Response{Message: golm.AssistantText("ok"), StopReason: golm.StopEndTurn}, nil
}
func (g *gateProvider) Stream(ctx context.Context, r golm.Request, _ func(golm.StreamEvent) error) (golm.Response, error) {
	return g.Complete(ctx, r)
}

func timeoutAfter() <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		<-contextTimeout()
		close(ch)
	}()
	return ch
}

func contextTimeout() <-chan struct{} {
	ctx, cancel := context.WithTimeout(context.Background(), 3e9)
	_ = cancel
	return ctx.Done()
}

// The lock must not outlive the requests using it.
func TestSessionLocksAreReleased(t *testing.T) {
	p := &echoProvider{}
	s := openaiapi.NewServer()
	s.Add("assistant", &golm.Agent{Provider: p, Model: "m"}, "")
	s.Store = sessionstore.NewFiles(t.TempDir())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	for i := 0; i < 20; i++ {
		resp := post(t, ts, "/v1/chat/completions", map[string]any{
			"model": "assistant", "golm_session": fmt.Sprintf("CONV%02d", i),
			"messages": []map[string]any{user("hi")},
		}, "")
		resp.Body.Close()
	}
	if n := openaiapi.LockCount(s); n != 0 {
		t.Errorf("%d session locks left behind", n)
	}
}
