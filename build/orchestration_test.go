// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package build_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/build"
)

func orchCfg(oc *golm.OrchestrationConfig) *golm.Config {
	return &golm.Config{
		Providers: []golm.ProviderConfig{{Name: "p", Type: "anthropic", APIKeyEnv: "X_KEY"}},
		Agents: []golm.PersonaConfig{
			{Name: "main", Provider: "p", Model: "claude-opus-5", Role: "main"},
			{Name: "researcher", Provider: "p", Model: "claude-opus-5", Role: "sub", Description: "looks things up"},
			{Name: "scanner", Provider: "p", Model: "claude-opus-5", Role: "sub"},
		},
		Orchestration: oc,
	}
}

func TestOrchestrationConfigReachesTheOrchestrator(t *testing.T) {
	rt, o, err := build.NewOrchestrator(context.Background(), build.Options{
		Config: orchCfg(&golm.OrchestrationConfig{
			Scope: "call", StreamDelegates: true, MaxDepth: 5,
			MaxConversations: 9, MaxFan: 7, FanParallel: 2,
		}),
	}, nil)
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	defer rt.Close()
	for _, c := range []struct {
		name string
		got  any
		want any
	}{
		{"Scope", o.Scope, golm.ScopeCall},
		{"StreamDelegates", o.StreamDelegates, true},
		{"MaxDepth", o.MaxDepth, 5},
		{"MaxConversations", o.MaxConversations, 9},
		{"MaxFan", o.MaxFan, 7},
		{"FanParallel", o.FanParallel, 2},
	} {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestOrchestrationDefaultsToRememberingWithinAConversation(t *testing.T) {
	rt, o, err := build.NewOrchestrator(context.Background(), build.Options{Config: orchCfg(nil)}, nil)
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	defer rt.Close()
	if o.Scope == golm.ScopeCall {
		t.Error("a config that says nothing should not silently forget between delegations")
	}
	if o.Router != nil {
		t.Error("no routes configured should mean no router")
	}
}

func TestConfiguredRoutesCompileIntoARouter(t *testing.T) {
	rt, o, err := build.NewOrchestrator(context.Background(), build.Options{
		Config: orchCfg(&golm.OrchestrationConfig{
			Routes: []golm.RouteRuleConfig{
				{Agent: "scanner", Prefix: "/scan"},
				{Agent: "researcher", Any: []string{"research", "look up"}},
			},
		}),
	}, nil)
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	defer rt.Close()
	if o.Router == nil {
		t.Fatal("configured routes produced no router")
	}
	for text, want := range map[string]string{
		"/scan the host":       "scanner",
		"please RESEARCH this": "researcher",
		"how are you":          "",
	} {
		got, err := o.Router(context.Background(), golm.RouteInput{Message: golm.UserText(text)})
		if err != nil {
			t.Fatalf("route %q: %v", text, err)
		}
		if got.Agent != want {
			t.Errorf("%q routed to %q, want %q", text, got.Agent, want)
		}
	}
}

func TestRouteToLastWrapsTheRules(t *testing.T) {
	rt, o, err := build.NewOrchestrator(context.Background(), build.Options{
		Config: orchCfg(&golm.OrchestrationConfig{
			RouteToLast: true,
			Routes:      []golm.RouteRuleConfig{{Agent: "researcher", Any: []string{"research"}}},
		}),
	}, nil)
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	defer rt.Close()
	sess := golm.NewSession()
	if err := sess.SetState("golm.last_agent", "scanner"); err != nil {
		t.Fatalf("SetState: %v", err)
	}

	got, err := o.Router(context.Background(), golm.RouteInput{Session: sess, Message: golm.UserText("research it")})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if got.Agent != "scanner" {
		t.Errorf("routed to %q, want the agent that answered last", got.Agent)
	}
}

func TestFanOutRegistersASecondWayIn(t *testing.T) {
	rt, o, err := build.NewOrchestrator(context.Background(), build.Options{
		Config: orchCfg(&golm.OrchestrationConfig{FanOut: true}),
	}, nil)
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	defer rt.Close()
	main := o.Main()
	for _, want := range []string{"researcher", "researcher_each", "scanner", "scanner_each"} {
		if _, ok := main.Tools.Get(want); !ok {
			t.Errorf("main agent has no %q tool", want)
		}
	}

	if _, ok := main.Tools.Get("main_each"); ok {
		t.Error("the main agent was given a fan-out tool onto itself")
	}
	each, _ := main.Tools.Get("researcher_each")
	if !strings.Contains(each.Description(), "looks things up") {
		t.Errorf("the fan tool lost the agent's description: %q", each.Description())
	}
}

func TestFanOutOffByDefault(t *testing.T) {
	rt, o, err := build.NewOrchestrator(context.Background(), build.Options{Config: orchCfg(nil)}, nil)
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	defer rt.Close()
	if _, ok := o.Main().Tools.Get("researcher_each"); ok {
		t.Error("fan-out tools were offered without being asked for")
	}
}

// A rule naming an agent nobody defined must be refused at load, not at the first turn that matches it.
func TestInvalidOrchestrationIsRefusedAtLoad(t *testing.T) {
	bad := []struct {
		name string
		oc   *golm.OrchestrationConfig
		want string
	}{
		{"unknown agent", &golm.OrchestrationConfig{Routes: []golm.RouteRuleConfig{{Agent: "nobody", Any: []string{"x"}}}}, "not a defined agent"},
		{"unknown scope", &golm.OrchestrationConfig{Scope: "forever"}, "want conversation or call"},
		{"rule with no condition", &golm.OrchestrationConfig{Routes: []golm.RouteRuleConfig{{Agent: "scanner"}}}, "no condition"},
		{"bad pattern", &golm.OrchestrationConfig{Routes: []golm.RouteRuleConfig{{Agent: "scanner", Pattern: "("}}}, "invalid pattern"},
		{"negative bound", &golm.OrchestrationConfig{MaxFan: -1}, "negative"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			err := orchCfg(c.oc).Validate()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want one mentioning %q", err, c.want)
			}
		})
	}
}

// The tools belong to the DEPLOYMENT, not to the persona.
func TestNewManySharesOneToolSet(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("- shared fact\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := orchCfg(nil)
	cfg.Memory = dir
	cfg.Builtin = &golm.BuiltinConfig{Workspace: t.TempDir(), WorkspaceReadOnly: true}

	rt, agents, err := build.NewMany(context.Background(), build.Options{Config: cfg})
	if err != nil {
		t.Fatalf("NewMany: %v", err)
	}
	defer rt.Close()

	if len(agents) != 3 {
		t.Fatalf("built %d agents, want 3", len(agents))
	}

	for name, ag := range agents {
		if ag.Name != name {
			t.Errorf("%s is named %q", name, ag.Name)
		}
		for _, want := range []string{"read_file", "remember"} {
			if _, ok := ag.Tools.Get(want); !ok {
				t.Errorf("%s has no %q tool", name, want)
			}
			if _, ok := rt.Tools.Get(want); !ok {
				t.Fatalf("the runtime itself has no %q tool", want)
			}
		}

		var sawMemory bool
		for _, sec := range ag.SystemPrompt.Sections() {
			if strings.Contains(sec.Text, "shared fact") {
				sawMemory = true
			}
		}
		if !sawMemory {
			t.Errorf("%s did not get the shared memory", name)
		}
	}
	if rt.Agent != agents["main"] {
		t.Error("Runtime.Agent should be the first name given")
	}

	if rt.Workspace == nil {
		t.Fatal("no workspace was opened")
	}
	if len(rt.Remotes) != len(cfg.MCPServers) {
		t.Errorf("%d MCP remotes for %d configured servers", len(rt.Remotes), len(cfg.MCPServers))
	}
}

func TestNewManyBuildsOnlyTheNamesAsked(t *testing.T) {
	rt, agents, err := build.NewMany(context.Background(),
		build.Options{Config: orchCfg(nil)}, "researcher")
	if err != nil {
		t.Fatalf("NewMany: %v", err)
	}
	defer rt.Close()
	if len(agents) != 1 || agents["researcher"] == nil {
		t.Errorf("built %v", agents)
	}
}

func TestNewManyRefusesAnUnknownName(t *testing.T) {
	_, _, err := build.NewMany(context.Background(), build.Options{Config: orchCfg(nil)}, "ghost")
	if err == nil || !strings.Contains(err.Error(), "not defined") {
		t.Errorf("err = %v", err)
	}
}

func TestNewManyRefusesAConfigWithNoAgents(t *testing.T) {
	cfg := orchCfg(nil)
	cfg.Agents = nil
	_, _, err := build.NewMany(context.Background(), build.Options{Config: cfg})
	if err == nil {
		t.Error("a config with no agents produced a runtime")
	}
}
