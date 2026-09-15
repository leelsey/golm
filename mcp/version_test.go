// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/leelsey/golm/internal/rpc"
)

func TestInitializeAcceptsOlderServerVersion(t *testing.T) {
	cEnd, sEnd := rpc.NewPipe()
	rs := rpc.NewServer()
	rs.Handle("initialize", func(context.Context, json.RawMessage) (any, error) {
		return InitializeResult{ProtocolVersion: "2024-11-05", ServerInfo: Implementation{Name: "old", Version: "1"}}, nil
	})
	rs.Handle("notifications/initialized", func(context.Context, json.RawMessage) (any, error) { return nil, nil })
	go func() { _ = rs.Serve(context.Background(), sEnd) }()

	c := NewClient(cEnd)
	if err := c.Initialize(context.Background(), "test-client"); err != nil {
		t.Fatalf("Initialize rejected an older server version: %v", err)
	}
	_ = c.Close()
}

// A client pinned to the revision most MCP hosts ship today must be met on it.
func TestServerAgreesWithACurrentClientVersion(t *testing.T) {
	for _, want := range []string{"2025-11-25", "2025-06-18"} {
		srv := NewServer("v", "1")
		params, _ := json.Marshal(InitializeParams{ProtocolVersion: want})
		res, err := srv.handleInitialize(context.Background(), params)
		if err != nil {
			t.Fatalf("%s: %v", want, err)
		}
		if got := res.(InitializeResult).ProtocolVersion; got != want {
			t.Errorf("client asked for %s, server answered %s", want, got)
		}
	}
}

// A revision this server cannot honour must NOT be agreed to.
func TestServerRefusesToClaimARevisionItCannotServe(t *testing.T) {
	for _, asked := range []string{"2025-03-26", "2024-11-05", "not-a-version"} {
		srv := NewServer("v", "1")
		params, _ := json.Marshal(InitializeParams{ProtocolVersion: asked})
		res, err := srv.handleInitialize(context.Background(), params)
		if err != nil {
			t.Fatalf("%s: %v", asked, err)
		}
		if got := res.(InitializeResult).ProtocolVersion; got != ProtocolVersion {
			t.Errorf("asked for %s, server agreed to %s; want its own %s", asked, got, ProtocolVersion)
		}
	}
}
