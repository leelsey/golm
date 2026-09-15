// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"testing"

	"github.com/leelsey/golm"
)

// A restriction must not be removable by a flag that does not mention it.
func TestWorkspaceFlagCannotUnsetConfiguredReadOnly(t *testing.T) {
	cfg := &golm.Config{Builtin: &golm.BuiltinConfig{Workspace: "/tmp/ws", WorkspaceReadOnly: true}}
	for _, flagged := range []string{"/tmp/ws", "/tmp/other"} {
		bf := &backendFlags{workspace: flagged}
		b := bf.builtin(cfg)
		if b == nil {
			t.Fatalf("--workspace %s produced no builtin block", flagged)
		}
		if b.Workspace != flagged {
			t.Errorf("workspace = %q, want the flag's %q", b.Workspace, flagged)
		}
		if !b.WorkspaceReadOnly {
			t.Errorf("--workspace %s dropped the configured read-only restriction", flagged)
		}
	}
}

func TestReadOnlyFlagLatchesOnToAConfiguredWorkspace(t *testing.T) {
	cfg := &golm.Config{Builtin: &golm.BuiltinConfig{Workspace: "/tmp/ws"}}
	b := (&backendFlags{readOnly: true}).builtin(cfg)
	if b == nil || b.Workspace != "/tmp/ws" || !b.WorkspaceReadOnly {
		t.Fatalf("--read-only over a configured workspace: %+v", b)
	}
}

// --fetch says WHETHER; it must not reset how.
func TestFetchFlagKeepsConfiguredLimits(t *testing.T) {
	cfg := &golm.Config{Builtin: &golm.BuiltinConfig{
		Fetch: &golm.FetchConfig{Timeout: "5s", MaxBytes: 4096},
	}}
	b := (&backendFlags{fetch: true}).builtin(cfg)
	if b.Fetch == nil {
		t.Fatal("no fetch block")
	}
	if b.Fetch.Timeout != "5s" || b.Fetch.MaxBytes != 4096 {
		t.Errorf("--fetch discarded the configured limits: %+v", b.Fetch)
	}
	if b.Fetch.AllowPrivate {
		t.Error("--fetch alone must not reach private addresses")
	}
	if cfg.Builtin.Fetch.AllowPrivate {
		t.Error("the flags mutated the parsed config")
	}
}

func TestFetchInternalLatchesAllowPrivate(t *testing.T) {
	cfg := &golm.Config{Builtin: &golm.BuiltinConfig{Fetch: &golm.FetchConfig{AllowPrivate: true}}}
	if b := (&backendFlags{fetch: true}).builtin(cfg); !b.Fetch.AllowPrivate {
		t.Error("--fetch must not narrow a configured allow_private away silently")
	}
	plain := &golm.Config{Builtin: &golm.BuiltinConfig{Fetch: &golm.FetchConfig{}}}
	if b := (&backendFlags{fetchInternal: true}).builtin(plain); !b.Fetch.AllowPrivate {
		t.Error("--fetch-internal did not take effect")
	}
}

// --allow-run is the allowlist and replaces it.
func TestAllowRunReplacesTheListButKeepsSettings(t *testing.T) {
	cfg := &golm.Config{Builtin: &golm.BuiltinConfig{
		Run: &golm.RunConfig{Allow: []string{"git"}, Timeout: "30s", InheritEnv: []string{"SSH_AUTH_SOCK"}},
	}}
	b := (&backendFlags{allowRun: stringList{"ls"}}).builtin(cfg)
	if b.Run == nil {
		t.Fatal("no run block")
	}
	if len(b.Run.Allow) != 1 || b.Run.Allow[0] != "ls" {
		t.Errorf("allow = %v, want exactly the flag's list", b.Run.Allow)
	}
	if b.Run.Timeout != "30s" || len(b.Run.InheritEnv) != 1 {
		t.Errorf("--allow-run discarded configured settings: %+v", b.Run)
	}
	if len(cfg.Builtin.Run.Allow) != 1 || cfg.Builtin.Run.Allow[0] != "git" {
		t.Error("the flags mutated the parsed config's allow list")
	}
}

func TestNoToolFlagsLeavesTheConfigInCharge(t *testing.T) {
	cfg := &golm.Config{Builtin: &golm.BuiltinConfig{Workspace: "/tmp/ws"}}
	if b := (&backendFlags{}).builtin(cfg); b != nil {
		t.Errorf("builtin = %+v, want nil so the config decides", b)
	}
}

func TestAskFlagGatesTheCapabilitiesThatMatter(t *testing.T) {
	pc := askPolicy()
	if !pc.NeedsApprover() {
		t.Fatal("--ask must produce a policy that asks")
	}
	for _, trait := range []string{"process", "network", "filesystem"} {
		var found bool
		for _, t2 := range pc.AskTraits {
			if t2 == trait {
				found = true
			}
		}
		if !found {
			t.Errorf("--ask does not gate %q", trait)
		}
	}
	if pc.Unreviewed != string(golm.DecisionAsk) {
		t.Error("--ask must gate tools that declare nothing")
	}
	if err := pc.Validate(""); err != nil {
		t.Errorf("--ask produces an invalid policy: %v", err)
	}
}
