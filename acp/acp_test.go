// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package acp_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leelsey/golm/acp"
	"github.com/leelsey/golm/internal/rpc"
)

type editor struct {
	peer *rpc.Peer

	mu      sync.Mutex
	updates []acp.SessionNotification

	permission func(acp.RequestPermissionRequest) acp.PermissionOutcome
	files      map[string]string
	writes     map[string]string
}

func (e *editor) record(n acp.SessionNotification) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.updates = append(e.updates, n)
}

func (e *editor) seen() []acp.SessionNotification {
	deadline := time.Now().Add(2 * time.Second)
	for {
		n := e.count()
		time.Sleep(5 * time.Millisecond)
		if e.count() == n || time.Now().After(deadline) {
			break
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]acp.SessionNotification(nil), e.updates...)
}

func (e *editor) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.updates)
}

func connect(t *testing.T, a *acp.Agent) *editor {
	t.Helper()
	ta, tb := rpc.NewPipe()
	e := &editor{files: map[string]string{}, writes: map[string]string{}}
	e.peer = rpc.NewPeer(tb)

	e.peer.Handle(acp.MethodSessionUpdate, func(_ context.Context, params json.RawMessage) (any, error) {
		var n struct {
			SessionID string          `json:"sessionId"`
			Update    json.RawMessage `json:"update"`
		}
		if err := json.Unmarshal(params, &n); err != nil {
			return nil, err
		}
		e.record(acp.SessionNotification{SessionID: n.SessionID, Update: n.Update})
		return nil, nil
	})
	e.peer.Handle(acp.MethodRequestPermission, func(_ context.Context, params json.RawMessage) (any, error) {
		var req acp.RequestPermissionRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		e.mu.Lock()
		decide := e.permission
		e.mu.Unlock()
		if decide == nil {
			return acp.RequestPermissionResponse{
				Outcome: acp.PermissionOutcome{Outcome: acp.OutcomeSelected, OptionID: "allow-once"},
			}, nil
		}
		return acp.RequestPermissionResponse{Outcome: decide(req)}, nil
	})
	e.peer.Handle(acp.MethodReadTextFile, func(_ context.Context, params json.RawMessage) (any, error) {
		var req acp.ReadTextFileRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		body, ok := e.files[req.Path]
		if !ok {
			return nil, errors.New("no such file in the editor: " + req.Path)
		}
		return acp.ReadTextFileResponse{Content: body}, nil
	})
	e.peer.Handle(acp.MethodWriteTextFile, func(_ context.Context, params json.RawMessage) (any, error) {
		var req acp.WriteTextFileRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		e.writes[req.Path] = req.Content
		return acp.WriteTextFileResponse{}, nil
	})

	agentDone := make(chan struct{})
	editorDone := make(chan struct{})
	go func() { defer close(agentDone); _ = a.Serve(context.Background(), ta) }()
	go func() { defer close(editorDone); _ = e.peer.Serve(context.Background()) }()
	t.Cleanup(func() {
		e.peer.Close()
		ta.Close()
		select {
		case <-agentDone:
		case <-time.After(3 * time.Second):
			t.Error("the agent did not stop")
		}
		<-editorDone
	})
	return e
}

func (e *editor) call(t *testing.T, method string, params, result any) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return e.peer.Call(ctx, method, params, result)
}

func (e *editor) initialize(t *testing.T, caps acp.ClientCapabilities) acp.InitializeResponse {
	t.Helper()
	var resp acp.InitializeResponse
	if err := e.call(t, acp.MethodInitialize, acp.InitializeRequest{
		ProtocolVersion:    acp.Version,
		ClientInfo:         &acp.Implementation{Name: "test-editor", Version: "1"},
		ClientCapabilities: caps,
	}, &resp); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	return resp
}

func (e *editor) newSession(t *testing.T) string {
	t.Helper()
	var resp acp.NewSessionResponse
	if err := e.call(t, acp.MethodNewSession, acp.NewSessionRequest{
		CWD: "/tmp/project", MCPServers: []acp.MCPServer{},
	}, &resp); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	return resp.SessionID
}

func (e *editor) prompt(t *testing.T, id, text string) acp.PromptResponse {
	t.Helper()
	var resp acp.PromptResponse
	if err := e.call(t, acp.MethodPrompt, acp.PromptRequest{
		SessionID: id, Prompt: []acp.ContentBlock{acp.TextBlock(text)},
	}, &resp); err != nil {
		t.Fatalf("session/prompt: %v", err)
	}
	return resp
}

func (e *editor) chunks(t *testing.T, kind string) string {
	t.Helper()
	var b strings.Builder
	for _, n := range e.seen() {
		raw, ok := n.Update.(json.RawMessage)
		if !ok {
			continue
		}
		var u struct {
			SessionUpdate string `json:"sessionUpdate"`
			Content       struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &u); err != nil {
			continue
		}
		if u.SessionUpdate == kind {
			b.WriteString(u.Content.Text)
		}
	}
	return b.String()
}

func (e *editor) toolUpdates(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, n := range e.seen() {
		raw, ok := n.Update.(json.RawMessage)
		if !ok {
			continue
		}
		var u map[string]any
		if err := json.Unmarshal(raw, &u); err != nil {
			continue
		}
		if k, _ := u["sessionUpdate"].(string); k == "tool_call" || k == "tool_call_update" {
			out = append(out, u)
		}
	}
	return out
}
