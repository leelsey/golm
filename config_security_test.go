// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validSaveCfg() *Config {
	return &Config{
		DefaultAgent: "m",
		Providers:    []ProviderConfig{{Name: "c", Type: "anthropic", APIKeyEnv: "K"}},
		Agents:       []PersonaConfig{{Name: "m", Provider: "c", Model: "x"}},
	}
}

func TestConfigSaveIgnoresStaleLoosePermTemp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p+".tmp", []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validSaveCfg().Save(p); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %o, want 0600 (stale temp leaked its mode)", fi.Mode().Perm())
	}
}

func TestConfigSaveDoesNotFollowSymlinkTemp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, p+".tmp"); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := validSaveCfg().Save(p); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "original" {
		t.Errorf("Save followed the symlink and overwrote the victim: %q", b)
	}
}

func TestValidateRejectsBadPromptVia(t *testing.T) {
	c := &Config{
		DefaultAgent: "m",
		Providers:    []ProviderConfig{{Name: "x", Type: "cli", Command: "echo", PromptVia: "args"}},
		Agents:       []PersonaConfig{{Name: "m", Provider: "x"}},
	}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "prompt_via") {
		t.Fatalf("expected a prompt_via validation error, got %v", err)
	}
}
