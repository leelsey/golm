// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package build

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/internal/rpc"
	"github.com/leelsey/golm/mcp"
)

const mcpServerEnv = "GOLM_BUILD_TEST_MCP_SERVER"

func TestMain(m *testing.M) {
	if len(os.Args) == 1 && os.Getenv(mcpServerEnv) == "" {
		fmt.Fprintf(os.Stderr, "re-executed without %s: the server marker did not reach the child; "+
			"name it in MCPServerConfig.InheritEnv\n", mcpServerEnv)
		os.Exit(2)
	}
	if os.Getenv(mcpServerEnv) != "" {
		srv := mcp.NewServer("build-test", "test")
		srv.AddTools(golm.TextTool("ping", "answers pong", "input", "anything",
			func(context.Context, string) (string, error) { return "pong", nil }))
		if err := srv.Serve(context.Background(), rpc.Stdio()); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestMCPToolsConnectsEveryServer(t *testing.T) {
	t.Setenv(mcpServerEnv, "1")
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}

	cfg := &golm.Config{MCPServers: []golm.MCPServerConfig{
		{Name: "one", Command: self, InheritEnv: []string{mcpServerEnv}},
		{Name: "two", Command: self, InheritEnv: []string{mcpServerEnv}},
	}}

	tools, closeAll, err := MCPTools(context.Background(), cfg)
	if err != nil {
		t.Fatalf("MCPTools: %v", err)
	}
	defer closeAll()

	if len(tools) != 2 {
		t.Fatalf("tools = %d, want 2 (one per server): %v", len(tools), names(tools))
	}
	out, err := tools[0].Execute(context.Background(), json.RawMessage(`{"input":"hi"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := (golm.ToolResult{Content: out}).Text(); got != "pong" {
		t.Errorf("tool returned %q, want %q", got, "pong")
	}
}

func TestMCPToolsFailsClosedOnABadServer(t *testing.T) {
	cfg := &golm.Config{MCPServers: []golm.MCPServerConfig{
		{Name: "missing", Command: "definitely-not-a-command-golm"},
	}}
	tools, closeAll, err := MCPTools(context.Background(), cfg)
	if err == nil {
		if closeAll != nil {
			closeAll()
		}
		t.Fatal("MCPTools with an undialable server should fail")
	}
	if tools != nil || closeAll != nil {
		t.Error("a failed MCPTools must return nothing to use or to close")
	}
}

func TestMCPToolsWithNoServers(t *testing.T) {
	tools, closeAll, err := MCPTools(context.Background(), &golm.Config{})
	if err != nil {
		t.Fatalf("MCPTools: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("tools = %v, want none", names(tools))
	}
	closeAll()
}

func names(tools []golm.Tool) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name()
	}
	return out
}
