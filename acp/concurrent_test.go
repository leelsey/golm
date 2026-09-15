// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package acp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/acp"
	"github.com/leelsey/golm/sessionstore"
)

// Two prompts on ONE session must not both claim the turn.
func TestSecondPromptOnOneSessionIsRefused(t *testing.T) {
	release := make(chan struct{})
	p := &scripted{responses: []golm.Response{say("first")}, block: release}
	a := acp.NewAgent(&golm.Agent{Provider: p, Model: "m"})
	e := connect(t, a)
	e.initialize(t, fullCaps())
	id := e.newSession(t)

	firstDone := make(chan acp.PromptResponse, 1)
	go func() {
		var resp acp.PromptResponse
		_ = e.call(t, acp.MethodPrompt, acp.PromptRequest{
			SessionID: id, Prompt: []acp.ContentBlock{acp.TextBlock("first")},
		}, &resp)
		firstDone <- resp
	}()
	time.Sleep(150 * time.Millisecond)

	var second acp.PromptResponse
	err := e.call(t, acp.MethodPrompt, acp.PromptRequest{
		SessionID: id, Prompt: []acp.ContentBlock{acp.TextBlock("second")},
	}, &second)
	if err == nil {
		t.Fatal("a second prompt was accepted while a turn was running")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("err = %v, want one saying a turn is in flight", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := e.peer.Notify(ctx, acp.MethodCancel, acp.CancelNotification{SessionID: id}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	select {
	case resp := <-firstDone:
		if resp.StopReason != acp.StopCancelled {
			t.Errorf("the first turn ended %q, want cancelled — the stop button reached the wrong turn",
				resp.StopReason)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the running turn could not be cancelled")
	}
	close(release)
}

// Sessions accumulate for the life of a connection.
func TestSessionsAreBounded(t *testing.T) {
	a, _ := newAgent(t, say("x"))
	a.MaxSessions = 3
	e := connect(t, a)
	e.initialize(t, fullCaps())

	var ids []string
	for i := 0; i < 6; i++ {
		ids = append(ids, e.newSession(t))
	}

	if r := e.prompt(t, ids[5], "hi"); r.StopReason != acp.StopEndTurn {
		t.Errorf("the newest session stopped with %q", r.StopReason)
	}

	var resp acp.PromptResponse
	err := e.call(t, acp.MethodPrompt, acp.PromptRequest{
		SessionID: ids[0], Prompt: []acp.ContentBlock{acp.TextBlock("hi")},
	}, &resp)
	if err == nil {
		t.Error("an evicted session still answered")
	}
}

// Reloading a session with a turn in flight would orphan it.
func TestLoadDuringATurnIsRefused(t *testing.T) {
	store := sessionstore.NewFiles(t.TempDir())
	release := make(chan struct{})
	p := &scripted{responses: []golm.Response{say("slow")}, block: release}
	a := acp.NewAgent(&golm.Agent{Provider: p, Model: "m"})
	a.Store = store
	e := connect(t, a)
	e.initialize(t, fullCaps())
	id := e.newSession(t)

	if err := store.Save(context.Background(), golm.SessionData{ID: id}.Session()); err != nil {
		t.Fatal(err)
	}

	go func() {
		var resp acp.PromptResponse
		_ = e.call(t, acp.MethodPrompt, acp.PromptRequest{
			SessionID: id, Prompt: []acp.ContentBlock{acp.TextBlock("go")},
		}, &resp)
	}()
	time.Sleep(150 * time.Millisecond)

	var loaded acp.LoadSessionResponse
	err := e.call(t, acp.MethodLoadSession, acp.LoadSessionRequest{
		SessionID: id, CWD: "/tmp", MCPServers: []acp.MCPServer{},
	}, &loaded)
	if err == nil {
		t.Fatal("a session was reloaded out from under a running turn")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("err = %v", err)
	}
	close(release)
}
