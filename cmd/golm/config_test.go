// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

func runArgs(args ...string) int {
	return run(args, strings.NewReader(""), io.Discard, io.Discard)
}

func TestConfigCRUD(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")

	if c := runArgs("config", "init", "--config", p); c != 0 {
		t.Fatalf("init: %d", c)
	}
	if c := runArgs("config", "add-provider", "--config", p, "--name", "claude", "--type", "anthropic", "--api-key-env", "ANTHROPIC_API_KEY"); c != 0 {
		t.Fatalf("add-provider: %d", c)
	}
	if c := runArgs("config", "add-agent", "--config", p, "--name", "main", "--provider", "claude", "--model", "claude-sonnet-4-6", "--default"); c != 0 {
		t.Fatalf("add-agent: %d", c)
	}

	c, err := golm.LoadConfig(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(c.Providers) != 1 || len(c.Agents) != 1 || c.DefaultAgent != "main" {
		t.Fatalf("unexpected config: %+v", c)
	}

	if c := runArgs("config", "add-agent", "--config", p, "--name", "bad", "--provider", "nope", "--model", "m"); c == 0 {
		t.Errorf("add-agent with unknown provider should fail")
	}
	if c := runArgs("config", "rm-provider", "--config", p, "claude"); c == 0 {
		t.Errorf("rm-provider while in use should fail")
	}

	if c := runArgs("config", "rm-agent", "--config", p, "main"); c != 0 {
		t.Errorf("rm-agent: %d", c)
	}
	if c := runArgs("config", "rm-provider", "--config", p, "claude"); c != 0 {
		t.Errorf("rm-provider: %d", c)
	}
	final, _ := golm.LoadConfig(p)
	if len(final.Providers) != 0 || len(final.Agents) != 0 || final.DefaultAgent != "" {
		t.Errorf("config not empty after removals: %+v", final)
	}
}

func TestConfigAPIKeyCmd(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	p := filepath.Join(t.TempDir(), "c.json")
	if c := runArgs("config", "add-provider", "--config", p, "--name", "local", "--type", "openai", "--base-url", srv.URL+"/v1", "--api-key-cmd", "echo secret-cmd-key"); c != 0 {
		t.Fatalf("add-provider: %d", c)
	}
	if c := runArgs("config", "add-agent", "--config", p, "--name", "main", "--provider", "local", "--model", "x", "--default"); c != 0 {
		t.Fatalf("add-agent: %d", c)
	}

	var out bytes.Buffer
	if c := run([]string{"--config", p, "--no-stream", "ping"}, strings.NewReader(""), &out, io.Discard); c != 0 {
		t.Fatalf("run: %d out=%q", c, out.String())
	}
	if gotAuth != "Bearer secret-cmd-key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer secret-cmd-key")
	}
}

func TestConfigAutoDiscover(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	if c := runArgs("config", "add-provider", "--config", p, "--name", "local", "--type", "cli", "--command", "cat"); c != 0 {
		t.Fatalf("add-provider: %d", c)
	}
	if c := runArgs("config", "add-agent", "--config", p, "--name", "main", "--provider", "local", "--model", "cli", "--default"); c != 0 {
		t.Fatalf("add-agent: %d", c)
	}

	t.Setenv("GOLM_CONFIG", p)
	var out bytes.Buffer
	if c := run([]string{"ping-discover"}, strings.NewReader(""), &out, io.Discard); c != 0 {
		t.Fatalf("run: %d out=%q", c, out.String())
	}
	if !strings.Contains(out.String(), "ping-discover") {
		t.Errorf("auto-discovered cli agent did not echo prompt: %q", out.String())
	}
}

func TestConfigSystemAndThinkOverride(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	const cfg = `{
  "default_agent": "main",
  "providers": [{"name": "local", "type": "cli", "command": "cat"}],
  "agents": [{"name": "main", "provider": "local", "model": "cli", "system": "persona system", "thinking": "auto"}]
}`
	if err := os.WriteFile(p, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	agent, err := testAgent(t, p, &backendFlags{provider: "anthropic", system: "override system"})
	if err != nil {
		t.Fatalf("buildAgent: %v", err)
	}
	if agent.System != "override system" {
		t.Errorf("System = %q, want %q", agent.System, "override system")
	}
	if agent.Thinking.Mode != golm.ThinkingAuto {
		t.Errorf("Thinking.Mode = %v, want persona's ThinkingAuto", agent.Thinking.Mode)
	}

	agent, err = testAgent(t, p, &backendFlags{provider: "anthropic"})
	if err != nil {
		t.Fatalf("buildAgent: %v", err)
	}
	if agent.System != "persona system" {
		t.Errorf("System = %q, want persona's %q", agent.System, "persona system")
	}

	agent, err = testAgent(t, p, &backendFlags{provider: "anthropic", think: "off"})
	if err != nil {
		t.Fatalf("buildAgent: %v", err)
	}
	if agent.Thinking.Mode != golm.ThinkingOff {
		t.Errorf("Thinking.Mode = %v, want ThinkingOff from explicit --think off", agent.Thinking.Mode)
	}

	agent, err = testAgent(t, p, &backendFlags{provider: "anthropic", think: "budget"})
	if err != nil {
		t.Fatalf("buildAgent: %v", err)
	}
	if agent.Thinking.Mode != golm.ThinkingBudget {
		t.Errorf("Thinking.Mode = %v, want ThinkingBudget from --think override", agent.Thinking.Mode)
	}
}

func TestConfigAddAgentResponseModality(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	const base = `{
  "providers": [{"name": "p", "type": "cli", "command": "cat"}],
  "agents": []
}`
	if err := os.WriteFile(p, []byte(base), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	var out, errb bytes.Buffer
	code := configAddAgent([]string{
		"--config", p, "--name", "a", "--provider", "p", "--model", "m",
		"--response-modality", "audio", "--response-modality", "image",
	}, &out, &errb)
	if code != 0 {
		t.Fatalf("add-agent failed: code=%d stderr=%s", code, errb.String())
	}
	cfg, err := golm.LoadConfig(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	a, ok := cfg.Agent("a")
	if !ok {
		t.Fatal("agent a not found")
	}
	if len(a.ResponseModalities) != 2 || a.ResponseModalities[0] != "audio" || a.ResponseModalities[1] != "image" {
		t.Errorf("ResponseModalities = %v, want [audio image]", a.ResponseModalities)
	}
}

func testAgent(t *testing.T, cfgPath string, bf *backendFlags) (*golm.Agent, error) {
	t.Helper()
	rt, err := bf.runtime(context.Background(), cfgPath, io.Discard)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { rt.Close() })
	return rt.Agent, nil
}
