// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package tools

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/leelsey/golm"
)

func runTool(t *testing.T, r *Runner, args runArgs) (string, error) {
	t.Helper()
	tool := r.Tool()
	if tool == nil {
		t.Fatal("no run tool")
	}
	b, _ := json.Marshal(args)
	out, err := tool.Execute(context.Background(), b)
	if err != nil {
		return "", err
	}
	return (golm.ToolResult{Content: out}).Text(), nil
}

// Without an allowlist there is no tool at all.
func TestRunIsNotOfferedWithoutAnAllowlist(t *testing.T) {
	if (&Runner{}).Tool() != nil {
		t.Error("a runner with no allowlist must offer nothing")
	}
	var nilRunner *Runner
	if nilRunner.Tool() != nil {
		t.Error("a nil runner must offer nothing")
	}
	if (&Runner{Allow: []string{"echo"}}).Tool() == nil {
		t.Error("an allowlist should produce a tool")
	}
}

func TestRunHonoursTheAllowlist(t *testing.T) {
	r := &Runner{Allow: []string{"echo"}}
	got, err := runTool(t, r, runArgs{Command: "echo", Args: []string{"hello"}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.TrimSpace(got) != "hello" {
		t.Errorf("output = %q", got)
	}

	_, err = runTool(t, r, runArgs{Command: "cat", Args: []string{"/etc/passwd"}})
	if err == nil {
		t.Fatal("a command outside the allowlist should be refused")
	}
	if !strings.Contains(err.Error(), "not allowed") || !strings.Contains(err.Error(), "echo") {
		t.Errorf("err = %v, want the refusal and what is allowed", err)
	}

	if _, err := runTool(t, r, runArgs{Command: "echoes"}); err == nil {
		t.Error("a prefix match should not pass the allowlist")
	}
	if _, err := runTool(t, r, runArgs{Command: "  "}); err == nil {
		t.Error("an empty command should be refused")
	}
}

// No shell is involved, so an argument that looks like shell metacharacters is just an argument.
func TestRunUsesNoShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs POSIX echo")
	}
	r := &Runner{Allow: []string{"echo"}}
	got, err := runTool(t, r, runArgs{Command: "echo", Args: []string{"a; rm -rf /; echo b"}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.TrimSpace(got) != "a; rm -rf /; echo b" {
		t.Errorf("output = %q, want the argument echoed verbatim", got)
	}
}

// The parent's environment is where the provider API keys live.
func TestRunDoesNotHandOverTheParentEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-should-not-escape")
	t.Setenv("GOLM_HARMLESS", "fine")

	r := &Runner{Allow: []string{"sh"}}
	got, err := runTool(t, r, runArgs{Command: "sh", Args: []string{"-c", "env"}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(got, "sk-should-not-escape") {
		t.Error("the child was given the parent's secrets")
	}
	if !strings.Contains(got, "PATH=") {
		t.Error("the child needs a PATH to be useful")
	}

	r.Inherit = []string{"GOLM_HARMLESS"}
	got, err = runTool(t, r, runArgs{Command: "sh", Args: []string{"-c", "env"}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, "GOLM_HARMLESS=fine") {
		t.Error("an inherited variable should reach the child")
	}
	if strings.Contains(got, "sk-should-not-escape") {
		t.Error("inheriting one variable must not bring the rest")
	}
}

func TestRunBoundsOutputAndTime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	r := &Runner{Allow: []string{"sh"}}
	got, err := runTool(t, r, runArgs{Command: "sh", Args: []string{"-c", "yes abcdefgh | head -c 400000"}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(got) > maxRunOutput+256 {
		t.Errorf("output = %d bytes, want it capped near %d", len(got), maxRunOutput)
	}
	if !strings.Contains(got, "truncated") {
		t.Error("truncated output must say so")
	}

	r = &Runner{Allow: []string{"sh"}, Timeout: 100 * time.Millisecond}
	start := time.Now()
	_, err = runTool(t, r, runArgs{Command: "sh", Args: []string{"-c", "sleep 30"}})
	if err == nil {
		t.Fatal("a command past its deadline should fail")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("err = %v, want the timeout named", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("waited %v; the deadline did not cut it short", elapsed)
	}
}

func TestRunReturnsWhatAFailingCommandPrinted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	r := &Runner{Allow: []string{"sh"}}
	_, err := runTool(t, r, runArgs{Command: "sh", Args: []string{"-c", "echo the reason >&2; exit 2"}})
	if err == nil {
		t.Fatal("a non-zero exit should be an error")
	}

	if !strings.Contains(err.Error(), "the reason") {
		t.Errorf("err = %v, want the command's own output", err)
	}
}

func TestRunDeclaresItsTraits(t *testing.T) {
	tr, ok := golm.TraitsOf((&Runner{Allow: []string{"echo"}}).Tool())
	if !ok {
		t.Fatal("run should declare its traits")
	}
	if !tr.Process || tr.ReadOnly {
		t.Errorf("traits = %+v, want Process and not read-only", tr)
	}

	if !strings.Contains((&Runner{Allow: []string{"git", "go"}}).Tool().Description(), "git, go") {
		t.Error("the description should list the allowed programs")
	}
}
