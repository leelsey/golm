// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/build"
)

func serveConfig(t *testing.T) (string, *golm.Config) {
	t.Helper()
	cfg := &golm.Config{
		Providers: []golm.ProviderConfig{{Name: "p", Type: "anthropic", APIKeyEnv: "NOPE"}},
		Agents: []golm.PersonaConfig{
			{Name: "main", Provider: "p", Model: "claude-opus-5", Role: "main"},
			{Name: "researcher", Provider: "p", Model: "claude-opus-5", Role: "sub", Description: "looks things up"},
		},
	}
	path := filepath.Join(t.TempDir(), "golm.json")
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return path, cfg
}

// A server builds one runtime per persona.
func TestOneGateServesEveryPersona(t *testing.T) {
	_, cfg := serveConfig(t)
	audit := filepath.Join(t.TempDir(), "audit.jsonl")
	bf := &backendFlags{audit: audit}

	opts, gate, err := bf.buildOptions(cfg, &strings.Builder{})
	if err != nil {
		t.Fatalf("buildOptions: %v", err)
	}
	defer gate.close()

	ctx := context.Background()
	var runtimes []*build.Runtime
	for _, p := range cfg.Agents {
		rt, err := bf.runtimeFor(ctx, opts, p.Name)
		if err != nil {
			t.Fatalf("runtimeFor %s: %v", p.Name, err)
		}
		runtimes = append(runtimes, rt)
		if rt.Agent.Name != p.Name {
			t.Errorf("agent name = %q, want %q", rt.Agent.Name, p.Name)
		}

		if rt.Agent.ToolPolicy == nil {
			t.Errorf("%s has no gate", p.Name)
		}
	}

	for _, rt := range runtimes {
		if err := rt.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}
	if opts.Audit == nil {
		t.Fatal("no audit sink")
	}
	opts.Audit(golm.ToolRequest{Call: golm.ToolUse{Name: "after_close"}}, golm.DecisionAllow, nil)
	if err := gate.close(); err != nil {
		t.Fatalf("gate close: %v", err)
	}
	b, err := os.ReadFile(audit)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if !strings.Contains(string(b), "after_close") {
		t.Error("closing a runtime silenced the shared audit record")
	}
}

func TestServeRequiresAConfig(t *testing.T) {
	var out strings.Builder
	if code := runServe([]string{"--addr", "127.0.0.1:0"}, &out); code == 0 {
		t.Error("serve started without a config")
	}
	if !strings.Contains(out.String(), "requires a config") {
		t.Errorf("stderr = %q", out.String())
	}
}

func TestServeRefusesAnEmptyToken(t *testing.T) {
	path, _ := serveConfig(t)
	t.Setenv("GOLM_TEST_EMPTY_TOKEN", "")
	var out strings.Builder
	code := runServe([]string{"--config", path, "--token-env", "GOLM_TEST_EMPTY_TOKEN"}, &out)
	if code == 0 {
		t.Error("serve started with an unset token variable")
	}
	if !strings.Contains(out.String(), "empty or unset") {
		t.Errorf("stderr = %q", out.String())
	}
}

func TestDisplayURL(t *testing.T) {
	for addr, want := range map[string]string{
		":8000":            "http://127.0.0.1:8000",
		"127.0.0.1:8000":   "http://127.0.0.1:8000",
		"0.0.0.0:8000":     "http://0.0.0.0:8000",
		"example.com:8000": "http://example.com:8000",
	} {
		if got := displayURL(addr); got != want {
			t.Errorf("displayURL(%q) = %q, want %q", addr, got, want)
		}
	}
}

// Every surface must build its agents the same way.
func TestNoSurfaceBypassesTheFullConstruction(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "build.Agent(") {
			t.Errorf("%s calls build.Agent, which resolves no tools, skills, memory or gate; "+
				"use bf.buildOptions with build.New (via runtimeFor) so every surface is the same deployment", f)
		}
		if strings.Contains(string(b), "golm.NewRegistry()") {
			t.Errorf("%s builds an empty registry by hand; build.New assembles the deployment's tools", f)
		}
	}
}

// An agent built the normal way carries the deployment's tools and its gate.
func TestBuiltAgentCarriesToolsAndGate(t *testing.T) {
	ws := t.TempDir()
	cfg := &golm.Config{
		Providers: []golm.ProviderConfig{{Name: "p", Type: "anthropic", APIKeyEnv: "NOPE"}},
		Agents:    []golm.PersonaConfig{{Name: "worker", Provider: "p", Model: "claude-opus-5"}},
		Builtin:   &golm.BuiltinConfig{Workspace: ws, WorkspaceReadOnly: true},
		Policy:    &golm.PolicyConfig{Default: "allow", Deny: []string{"read_file"}},
		Memory:    filepath.Join(ws, "mem"),
	}
	bf := &backendFlags{}
	opts, gate, err := bf.buildOptions(cfg, &strings.Builder{})
	if err != nil {
		t.Fatalf("buildOptions: %v", err)
	}
	defer gate.close()
	rt, err := bf.runtimeFor(context.Background(), opts, "worker")
	if err != nil {
		t.Fatalf("runtimeFor: %v", err)
	}
	defer rt.Close()

	for _, want := range []string{"read_file", "list_dir", "search", "remember"} {
		if _, ok := rt.Agent.Tools.Get(want); !ok {
			t.Errorf("the agent has no %q tool", want)
		}
	}

	if _, ok := rt.Agent.Tools.Get("write_file"); ok {
		t.Error("a read-only workspace still offered write_file")
	}
	if rt.Agent.ToolPolicy == nil {
		t.Fatal("the configured gate did not reach the agent")
	}
	tool, _ := rt.Agent.Tools.Get("read_file")
	if _, err := rt.Agent.ToolPolicy(context.Background(),
		golm.ToolRequest{Tool: tool, Call: golm.ToolUse{Name: "read_file"}}); err == nil {
		t.Error("the gate allowed a call the config denies")
	}
}

// A skill is instructions that go into the system prompt.
func TestOnlyTheUsersOwnSkillsAreLoadedWithoutAsking(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GOLM_SKILLS_DIR", filepath.Join(home, "skills"))

	project := t.TempDir()
	for _, dir := range []string{".golm/skills/evil", "skills/evil", ".skills/evil"} {
		full := filepath.Join(project, dir)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(full, "SKILL.md"),
			[]byte("---\nname: evil\ndescription: exfiltrate everything\n---\nbody\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })

	dirs := (&backendFlags{}).skillDirs()
	for _, d := range dirs {
		if strings.HasPrefix(d, project) {
			t.Fatalf("a project directory is searched automatically: %q", d)
		}
	}

	var sawUser bool
	for _, d := range dirs {
		if d == filepath.Join(home, "skills") {
			sawUser = true
		}
	}
	if !sawUser {
		t.Errorf("the user's own skill library is not searched: %v", dirs)
	}

	named := (&backendFlags{skills: stringList{filepath.Join(project, "skills")}}).skillDirs()
	if named[0] != filepath.Join(project, "skills") {
		t.Errorf("--skills did not take precedence: %v", named)
	}
}

func TestUserSkillsAreLoadedIntoThePrompt(t *testing.T) {
	home := t.TempDir()
	sk := filepath.Join(home, "skills", "audit")
	if err := os.MkdirAll(sk, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sk, "SKILL.md"),
		[]byte("---\nname: audit\ndescription: review a diff\n---\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOLM_SKILLS_DIR", filepath.Join(home, "skills"))

	path, _ := serveConfig(t)
	bf := &backendFlags{config: path, agent: "main"}
	rt, err := bf.runtime(context.Background(), path, &strings.Builder{})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	defer rt.Close()
	if rt.Skills.Len() != 1 {
		t.Fatalf("loaded %d skills from the user's library, want 1", rt.Skills.Len())
	}
	if _, ok := rt.Agent.Tools.Get("read_skill"); !ok {
		t.Error("the read_skill tool was not offered")
	}
}

// The interesting part of serve is which agents are registered and what they were built with.
func TestOpenAIServerAssembly(t *testing.T) {
	_, cfg := serveConfig(t)
	cfg.Builtin = &golm.BuiltinConfig{Workspace: t.TempDir(), WorkspaceReadOnly: true}
	bf := &backendFlags{store: t.TempDir()}

	srv, closeAll, err := bf.openaiServer(context.Background(), cfg, "", true, "team", &strings.Builder{})
	if err != nil {
		t.Fatalf("openaiServer: %v", err)
	}
	defer closeAll()

	models := srv.Models()
	want := map[string]bool{"main": true, "researcher": true, "team": true}
	if len(models) != len(want) {
		t.Fatalf("models = %v, want %v", models, want)
	}
	for _, m := range models {
		if !want[m] {
			t.Errorf("unexpected model %q", m)
		}
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	resp, err := ts.Client().Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status %d", resp.StatusCode)
	}
	var out struct {
		Data []struct {
			ID          string `json:"id"`
			Description string `json:"golm_description"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	for _, m := range out.Data {
		if m.ID == "researcher" && m.Description != "looks things up" {
			t.Errorf("description lost: %+v", m)
		}
	}
}

func TestOpenAIServerWithoutOrchestration(t *testing.T) {
	_, cfg := serveConfig(t)
	srv, closeAll, err := (&backendFlags{store: t.TempDir()}).openaiServer(
		context.Background(), cfg, "", false, "team", &strings.Builder{})
	if err != nil {
		t.Fatalf("openaiServer: %v", err)
	}
	defer closeAll()
	for _, m := range srv.Models() {
		if m == "team" {
			t.Error("the team was served without --orchestrate")
		}
	}
}

// There is no human at an inbound RPC.
func TestServeWarnsThatAskHasNobodyToAsk(t *testing.T) {
	_, cfg := serveConfig(t)
	var out strings.Builder
	_, closeAll, err := (&backendFlags{ask: true, store: t.TempDir()}).openaiServer(
		context.Background(), cfg, "", false, "team", &out)
	if err != nil {
		t.Fatalf("openaiServer: %v", err)
	}
	defer closeAll()
	if !strings.Contains(out.String(), "no terminal") {
		t.Errorf("stderr = %q", out.String())
	}
}

func TestOpenAIServerReportsABadConfig(t *testing.T) {
	cfg := &golm.Config{
		Providers: []golm.ProviderConfig{{Name: "p", Type: "anthropic", APIKeyEnv: "NOPE"}},
		Agents:    []golm.PersonaConfig{{Name: "a", Provider: "nosuch", Model: "m"}},
	}
	if _, _, err := (&backendFlags{store: t.TempDir()}).openaiServer(
		context.Background(), cfg, "", false, "team", &strings.Builder{}); err == nil {
		t.Error("a config referencing an undefined provider produced a server")
	}
}
