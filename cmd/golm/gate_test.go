// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

// --ask compiles the trait-based policy the CLI promises.
func TestAskPolicyGatesWhatItSaysItDoes(t *testing.T) {
	p := askPolicy()
	for _, trait := range []string{"process", "network", "filesystem"} {
		if !contains(p.AskTraits, trait) {
			t.Errorf("--ask does not gate the %q trait", trait)
		}
	}

	if p.Unreviewed != string(golm.DecisionAsk) {
		t.Errorf("Unreviewed = %q, want ask: an undeclared tool would pass unasked", p.Unreviewed)
	}

	for _, tool := range []string{"read_file", "list_dir", "search", "read_skill"} {
		if !contains(p.Allow, tool) {
			t.Errorf("--ask asks about %q, which only reads; the prompts become noise", tool)
		}
	}
	for _, tool := range []string{"write_file", "edit_file", "run", "fetch"} {
		if contains(p.Allow, tool) {
			t.Errorf("--ask allows %q outright; that tool changes something", tool)
		}
	}
}

// A gate that cannot ask must refuse, not allow.
func TestGateWithoutATerminalLeavesNoApproverAndWarns(t *testing.T) {
	var errb strings.Builder
	bf := &backendFlags{ask: true}
	g, err := bf.gate(nil, &errb)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if g.policy == nil {
		t.Fatal("--ask produced no policy")
	}
	if !g.policy.NeedsApprover() {
		t.Fatal("the --ask policy does not need an approver; nothing would ever be asked")
	}
	if g.approve == nil {
		if !strings.Contains(errb.String(), "no terminal") {
			t.Errorf("no approver and no warning: %q", errb.String())
		}
	}

	rules := g.policy.Rules()
	if rules == nil {
		t.Fatal("the policy did not compile")
	}
	rules.Approve = g.approve
	if rules.Approve == nil {
		pol := rules.Policy()
		_, err := pol(t.Context(), golm.ToolRequest{Call: golm.ToolUse{Name: "run"}})
		if err == nil {
			t.Error("a gated call was allowed with no approver; the gate fell open")
		}
	}
}

// The config's own policy is used when --ask is not given.
func TestGatePrefersAskOverTheConfigPolicy(t *testing.T) {
	cfg := &golm.Config{Policy: &golm.PolicyConfig{Default: string(golm.DecisionAllow)}}

	var errb strings.Builder
	plain, err := (&backendFlags{}).gate(cfg, &errb)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if plain.policy != nil {
		t.Error("without --ask the gate compiled a policy of its own")
	}

	asked, err := (&backendFlags{ask: true}).gate(cfg, &errb)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if asked.policy == nil || asked.policy.Unreviewed != string(golm.DecisionAsk) {
		t.Error("--ask did not take precedence over the config's allow-by-default policy")
	}
}

// --audit records every decision, allowed ones included.
func TestGateOpensAndClosesTheAuditLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	var errb strings.Builder

	bf := &backendFlags{audit: path}
	g, err := bf.gate(nil, &errb)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if g.audit == nil {
		t.Fatal("--audit produced no recorder")
	}
	g.audit(golm.ToolRequest{Call: golm.ToolUse{Name: "run", Input: []byte(`{}`)}}, golm.DecisionAllow, nil)
	if err := g.close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the audit log was not written: %v", err)
	}
	if !strings.Contains(string(b), "run") {
		t.Errorf("the allowed call was not recorded: %q", b)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("audit log mode is %v, want 0600", fi.Mode().Perm())
	}
}

// An audit path that cannot be opened must fail the gate, not produce one that silently records nothing.
func TestGateFailsWhenTheAuditLogCannotBeOpened(t *testing.T) {
	var errb strings.Builder
	bf := &backendFlags{audit: filepath.Join(t.TempDir(), "no-such-dir", "audit.jsonl")}
	if _, err := bf.gate(nil, &errb); err == nil {
		t.Fatal("an unopenable audit log produced a working gate")
	}
}

// buildOptions must not leak the audit handle when a LATER step fails.
func TestBuildOptionsReleasesTheAuditLogOnALaterFailure(t *testing.T) {
	dir := t.TempDir()
	var errb strings.Builder

	before := openDescriptors(t)
	for i := 0; i < 20; i++ {
		bf := &backendFlags{
			audit:    filepath.Join(dir, fmt.Sprintf("audit-%d.jsonl", i)),
			logLevel: "not-a-level",
		}
		if _, _, err := bf.buildOptions(nil, &errb); err == nil {
			t.Fatal("an invalid --log level produced working options")
		}
	}
	after := openDescriptors(t)
	if after > before+5 {
		t.Errorf("20 failed builds leaked descriptors: %d open before, %d after", before, after)
	}
}

func openDescriptors(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		t.Skip("cannot count descriptors on this host")
	}
	return len(entries)
}

func contains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}
