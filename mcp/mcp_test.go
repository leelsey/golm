// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/internal/rpc"
)

func echoTool() golm.Tool {
	return golm.NewTool("echo", "echoes its input", json.RawMessage(`{"type":"object"}`),
		func(_ context.Context, in json.RawMessage) (string, error) {
			return "echoed:" + string(in), nil
		})
}

func newPair(t *testing.T, srv *Server) *Client {
	t.Helper()
	cEnd, sEnd := rpc.NewPipe()
	go func() { _ = srv.Serve(context.Background(), sEnd) }()
	client := NewClient(cEnd)
	t.Cleanup(func() { client.Close() })
	if err := client.Initialize(context.Background(), "test-client"); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	return client
}

func TestServerToolsListAndCall(t *testing.T) {
	srv := NewServer("test-server", "0.1")
	srv.AddTools(echoTool())
	client := newPair(t, srv)

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools)
	}

	out, isErr, err := client.CallTool(context.Background(), "echo", json.RawMessage(`{"x":1}`))
	if err != nil || isErr {
		t.Fatalf("call: out=%q isErr=%v err=%v", out, isErr, err)
	}
	if out != `echoed:{"x":1}` {
		t.Errorf("call result = %q", out)
	}

	if _, isErr, _ := client.CallTool(context.Background(), "nope", nil); !isErr {
		t.Errorf("expected isError for unknown tool")
	}
}

func TestClientToolsBridge(t *testing.T) {
	srv := NewServer("test-server", "0.1")
	srv.AddTools(echoTool())
	client := newPair(t, srv)

	gtools, err := client.Tools(context.Background())
	if err != nil || len(gtools) != 1 {
		t.Fatalf("bridge tools = %d err=%v", len(gtools), err)
	}
	res, err := gtools[0].Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || len(res) != 1 || res[0].(golm.Text).Text != `echoed:{}` {
		t.Fatalf("bridged execute = %v err=%v", res, err)
	}
}

type stubProvider struct{}

func (stubProvider) Name() string                    { return "stub" }
func (stubProvider) Capabilities() golm.Capabilities { return golm.Capabilities{} }
func (stubProvider) Complete(_ context.Context, req golm.Request) (golm.Response, error) {
	last := req.Messages[len(req.Messages)-1].Text()
	return golm.Response{Message: golm.AssistantText("ran:" + last), StopReason: golm.StopEndTurn}, nil
}
func (s stubProvider) Stream(ctx context.Context, req golm.Request, fn func(golm.StreamEvent) error) (golm.Response, error) {
	return s.Complete(ctx, req)
}

func TestServerAgentAsTool(t *testing.T) {
	srv := NewServer("test-server", "0.1")
	srv.AddAgent("assistant", "runs the assistant agent", &golm.Agent{Provider: stubProvider{}, Model: "x"})
	client := newPair(t, srv)

	out, isErr, err := client.CallTool(context.Background(), "assistant", json.RawMessage(`{"input":"hello"}`))
	if err != nil || isErr {
		t.Fatalf("agent call: out=%q isErr=%v err=%v", out, isErr, err)
	}
	if out != "ran:hello" {
		t.Errorf("agent result = %q", out)
	}
}

// AddRegistry is the form an embedder reaches for.
func TestAddRegistryExposesEveryTool(t *testing.T) {
	reg := golm.NewRegistry()
	reg.Register(echoTool(),
		golm.TextTool("shout", "uppercases", "input", "text",
			func(_ context.Context, in string) (string, error) { return strings.ToUpper(in), nil }))

	srv := NewServer("test-server", "0.1")
	srv.AddRegistry(reg)
	client := newPair(t, srv)

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools = %d, want both of the registry's: %+v", len(tools), tools)
	}
	out, isErr, err := client.CallTool(context.Background(), "shout", json.RawMessage(`{"input":"hi"}`))
	if err != nil || isErr {
		t.Fatalf("call: %v isErr=%v", err, isErr)
	}
	if out != "HI" {
		t.Errorf("shout returned %q, want %q", out, "HI")
	}
}

// The warning on WithPolicy made concrete.
func TestAddRegistryStillAnswersToThePolicy(t *testing.T) {
	reg := golm.NewRegistry()
	reg.Register(echoTool())

	srv := NewServer("test-server", "0.1")
	srv.AddRegistry(reg)
	srv.WithPolicy(func(_ context.Context, req golm.ToolRequest) (context.Context, error) {
		return nil, fmt.Errorf("refused %q", req.Call.Name)
	})
	client := newPair(t, srv)

	out, isErr, err := client.CallTool(context.Background(), "echo", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !isErr {
		t.Fatal("a refused tool reported success; the gate does not cover AddRegistry")
	}
	if !strings.Contains(out, "refused") {
		t.Errorf("result = %q, want the refusal reason", out)
	}
}
