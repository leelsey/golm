// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/internal/rpc"
)

const (
	serverEnv  = "GOLM_MCP_LAZY_TEST_SERVER"
	startedEnv = "GOLM_MCP_LAZY_TEST_STARTED"
	toolsEnv   = "GOLM_MCP_LAZY_TEST_TOOLS"

	envProbeEnv = "GOLM_MCP_ENV_PROBE"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 1 && os.Getenv(serverEnv) == "" {
		fmt.Fprintf(os.Stderr, "re-executed without %s: the server marker did not reach the child; "+
			"name it in Remote.Inherit\n", serverEnv)
		os.Exit(2)
	}
	if os.Getenv(serverEnv) == "" {
		os.Exit(m.Run())
	}
	if p := os.Getenv(startedEnv); p != "" {
		f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			f.WriteString("start\n")
			f.Close()
		}
	}
	srv := NewServer("lazy-test", "test")
	if os.Getenv(envProbeEnv) != "" {
		srv.AddTools(golm.TextTool("getenv", "report one variable of this server's environment",
			"name", "the variable to read",
			func(_ context.Context, name string) (string, error) {
				return os.Getenv(name), nil
			}))
		if err := srv.Serve(context.Background(), rpc.Stdio()); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	names := []string{"alpha"}
	if os.Getenv(toolsEnv) != "" {
		names = []string{os.Getenv(toolsEnv)}
	}
	for _, n := range names {
		srv.AddTools(golm.TextTool(n, "returns "+n, "input", "anything",
			func(context.Context, string) (string, error) { return "ran " + n, nil }))
	}
	if err := srv.Serve(context.Background(), rpc.Stdio()); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func starts(t *testing.T, marker string) int {
	t.Helper()
	b, err := os.ReadFile(marker)
	if err != nil {
		return 0
	}
	n := 0
	for _, c := range b {
		if c == '\n' {
			n++
		}
	}
	return n
}

func newRemote(t *testing.T, cache, marker string) *Remote {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}
	t.Setenv(serverEnv, "1")
	t.Setenv(startedEnv, marker)

	return &Remote{Name: "probe", Command: self, CacheDir: cache,
		Inherit: []string{serverEnv, startedEnv, toolsEnv}}
}

func TestRemoteDiscoversThenServesFromTheManifest(t *testing.T) {
	cache := t.TempDir()
	marker := filepath.Join(t.TempDir(), "starts")

	cold := newRemote(t, cache, marker)
	tools, err := cold.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name() != "alpha" {
		t.Fatalf("tools = %v, want one named alpha", names(tools))
	}
	if got := starts(t, marker); got != 1 {
		t.Fatalf("server starts during discovery = %d, want 1", got)
	}
	if err := cold.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	warm := newRemote(t, cache, marker)
	defer warm.Close()
	tools, err = warm.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name() != "alpha" {
		t.Fatalf("tools from manifest = %v, want one named alpha", names(tools))
	}
	if warm.Connected() {
		t.Error("describing tools from the manifest must not connect")
	}
	if got := starts(t, marker); got != 1 {
		t.Errorf("server starts after a cached describe = %d, want it unchanged at 1", got)
	}

	out, err := tools[0].Execute(context.Background(), json.RawMessage(`{"input":"x"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := (golm.ToolResult{Content: out}).Text(); got != "ran alpha" {
		t.Errorf("result = %q, want %q", got, "ran alpha")
	}
	if !warm.Connected() {
		t.Error("calling a tool should have connected")
	}
	if got := starts(t, marker); got != 2 {
		t.Errorf("server starts after a call = %d, want 2", got)
	}
}

func TestRemoteManifestFollowsTheCommand(t *testing.T) {
	cache := t.TempDir()
	marker := filepath.Join(t.TempDir(), "starts")
	r := newRemote(t, cache, marker)
	if _, err := r.Tools(context.Background()); err != nil {
		t.Fatalf("Tools: %v", err)
	}
	r.Close()

	changed := newRemote(t, cache, marker)
	changed.Args = []string{"-extra"}
	defer changed.Close()
	if _, ok := changed.readManifest(); ok {
		t.Error("a manifest written for different arguments should not be used")
	}
}

func TestRemoteRefreshesAStaleManifestOnConnect(t *testing.T) {
	cache := t.TempDir()
	marker := filepath.Join(t.TempDir(), "starts")

	first := newRemote(t, cache, marker)
	tools, err := first.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	first.Close()

	t.Setenv(toolsEnv, "beta")
	warm := newRemote(t, cache, marker)
	defer warm.Close()
	stale, err := warm.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(stale) != 1 || stale[0].Name() != "alpha" {
		t.Fatalf("manifest = %v, want the cached alpha", names(stale))
	}

	_, err = stale[0].Execute(context.Background(), json.RawMessage(`{"input":"x"}`))
	if err == nil {
		t.Fatal("calling a tool the server no longer has should fail")
	}
	if !strings.Contains(err.Error(), "no longer offers") || !strings.Contains(err.Error(), "alpha") {
		t.Errorf("err = %v, want it to name the missing tool", err)
	}

	m, ok := warm.readManifest()
	if !ok || len(m.Tools) != 1 || m.Tools[0].Name != "beta" {
		t.Errorf("refreshed manifest = %+v, want it to describe beta", m)
	}
	_ = tools
}

func TestRemoteWithoutACacheStillWorks(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "starts")
	r := newRemote(t, "", marker)
	defer r.Close()
	tools, err := r.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools = %v, want one", names(tools))
	}
	if !r.Connected() {
		t.Error("without a cache the only way to describe tools is to connect")
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if err := r.Close(); err != nil {
		t.Errorf("second close: %v", err)
	}
}

func TestRemoteDialFailureNamesTheServer(t *testing.T) {
	r := &Remote{Name: "ghost", Command: "definitely-not-a-command-golm"}
	defer r.Close()
	_, err := r.Tools(context.Background())
	if err == nil {
		t.Fatal("an undialable server should fail")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("err = %v, want it to name the server", err)
	}
}

func names(tools []golm.Tool) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name()
	}
	return out
}
