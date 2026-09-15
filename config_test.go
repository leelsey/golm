// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "golm.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadConfigValid(t *testing.T) {
	p := writeTemp(t, `{
	  "default_agent": "main",
	  "providers": [
	    {"name": "claude", "type": "anthropic", "api_key_env": "ANTHROPIC_API_KEY"}
	  ],
	  "agents": [
	    {"name": "main", "provider": "claude", "model": "claude-x", "system": "be terse", "thinking": "budget", "thinking_budget": 1024}
	  ]
	}`)
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	a, ok := cfg.Agent("main")
	if !ok || a.Model != "claude-x" {
		t.Fatalf("agent not loaded: %+v", a)
	}
	tc := a.ThinkingConfig()
	if tc.Mode != ThinkingBudget || tc.Budget != 1024 {
		t.Errorf("thinking config = %+v", tc)
	}
}

func TestLoadConfigUnknownProvider(t *testing.T) {
	p := writeTemp(t, `{
	  "providers": [{"name": "claude", "type": "anthropic", "api_key_env": "K"}],
	  "agents": [{"name": "main", "provider": "ghost", "model": "m"}]
	}`)
	if _, err := LoadConfig(p); err == nil {
		t.Fatalf("expected validation error for unknown provider reference")
	}
}

func TestLoadConfigRejectsUnknownFields(t *testing.T) {
	p := writeTemp(t, `{"providers": [], "agents": [], "bogus": true}`)
	if _, err := LoadConfig(p); err == nil {
		t.Fatalf("expected error for unknown field")
	}
}
