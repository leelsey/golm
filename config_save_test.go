// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigSaveAtomic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")

	cfg := &Config{
		DefaultAgent: "main",
		Providers:    []ProviderConfig{{Name: "claude", Type: "anthropic", APIKeyEnv: "ANTHROPIC_API_KEY"}},
		Agents:       []PersonaConfig{{Name: "main", Provider: "claude", Model: "claude-x"}},
	}
	if err := cfg.Save(p); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file %s.tmp left behind after save", p)
	}

	got, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig after save: %v", err)
	}
	if a, ok := got.Agent("main"); !ok || a.Model != "claude-x" {
		t.Fatalf("reloaded agent = %+v, ok=%v", a, ok)
	}

	cfg.Agents[0].Model = "claude-y"
	if err := cfg.Save(p); err != nil {
		t.Fatalf("Save (overwrite): %v", err)
	}
	got2, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig after overwrite: %v", err)
	}
	if a, _ := got2.Agent("main"); a.Model != "claude-y" {
		t.Errorf("overwrite not applied: model = %q, want claude-y", a.Model)
	}
}
