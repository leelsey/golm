// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package build

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leelsey/golm"
)

// a2a_agents entries carry the NAME of the env var, never the token.
func TestA2AToolsSendsTheConfiguredToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{
				"id": "t1", "contextId": "c1", "kind": "task",
				"status": map[string]any{"state": "completed"},
				"artifacts": []any{map[string]any{
					"artifactId": "a1",
					"parts":      []any{map[string]any{"kind": "text", "text": "ok"}},
				}},
			},
		})
	}))
	defer srv.Close()

	t.Setenv("GOLM_TEST_A2A_TOKEN", "s3cret")
	cfg := &golm.Config{A2AAgents: []golm.A2AAgentConfig{
		{Name: "remote", URL: srv.URL, TokenEnv: "GOLM_TEST_A2A_TOKEN"},
	}}
	tools := A2ATools(cfg)
	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d", len(tools))
	}
	if _, err := tools[0].Execute(context.Background(), json.RawMessage(`{"task":"go"}`)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotAuth != "Bearer s3cret" {
		t.Errorf("Authorization = %q, want the configured token", gotAuth)
	}
}

// No token_env means no header.
func TestA2AToolsWithoutTokenEnvSendsNoAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{
				"id": "t1", "contextId": "c1", "kind": "task",
				"status": map[string]any{"state": "completed"},
			},
		})
	}))
	defer srv.Close()

	cfg := &golm.Config{A2AAgents: []golm.A2AAgentConfig{{Name: "remote", URL: srv.URL}}}
	if _, err := A2ATools(cfg)[0].Execute(context.Background(), json.RawMessage(`{"task":"go"}`)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization = %q, want none", gotAuth)
	}
}
