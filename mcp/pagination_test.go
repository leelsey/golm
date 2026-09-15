// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/leelsey/golm/internal/rpc"
)

func TestListToolsPagination(t *testing.T) {
	cEnd, sEnd := rpc.NewPipe()
	srv := rpc.NewServer()
	srv.Handle("tools/list", func(_ context.Context, params json.RawMessage) (any, error) {
		var p struct {
			Cursor string `json:"cursor"`
		}
		_ = json.Unmarshal(params, &p)
		if p.Cursor == "" {
			return ListToolsResult{Tools: []Tool{{Name: "a"}}, NextCursor: "c2"}, nil
		}
		return ListToolsResult{Tools: []Tool{{Name: "b"}}}, nil
	})
	go func() { _ = srv.Serve(context.Background(), sEnd) }()

	c := NewClient(cEnd)
	defer c.Close()
	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 {
		t.Errorf("got %d tools, want 2 across both pages", len(tools))
	}
}

func TestListToolsStuckCursorTerminates(t *testing.T) {
	cEnd, sEnd := rpc.NewPipe()
	srv := rpc.NewServer()
	srv.Handle("tools/list", func(context.Context, json.RawMessage) (any, error) {
		return ListToolsResult{Tools: []Tool{{Name: "a"}}, NextCursor: "stuck"}, nil
	})
	go func() { _ = srv.Serve(context.Background(), sEnd) }()

	c := NewClient(cEnd)
	defer c.Close()
	done := make(chan error, 1)
	go func() { _, err := c.ListTools(context.Background()); done <- err }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ListTools did not terminate on a stuck cursor")
	}
}

func TestServerInitializeReturnsSupportedVersion(t *testing.T) {
	s := NewServer("srv", "0.1")
	params, _ := json.Marshal(InitializeParams{ProtocolVersion: "1999-01-01"})
	res, err := s.handleInitialize(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.(InitializeResult).ProtocolVersion; got != ProtocolVersion {
		t.Errorf("server returned %q for an unsupported request, want %q", got, ProtocolVersion)
	}
}
