// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package grpc

import (
	"context"
	"encoding/json"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/a2a"
)

type stubProvider struct{}

func (stubProvider) Name() string                    { return "stub" }
func (stubProvider) Capabilities() golm.Capabilities { return golm.Capabilities{} }
func (stubProvider) Complete(_ context.Context, req golm.Request) (golm.Response, error) {
	return golm.Response{Message: golm.AssistantText("agent:" + req.Messages[len(req.Messages)-1].Text()), StopReason: golm.StopEndTurn}, nil
}
func (s stubProvider) Stream(ctx context.Context, req golm.Request, fn func(golm.StreamEvent) error) (golm.Response, error) {
	_ = fn(golm.StreamEvent{Type: golm.EventTextDelta, Text: "agent:" + req.Messages[len(req.Messages)-1].Text()})
	return golm.Response{Message: golm.AssistantText("agent:" + req.Messages[len(req.Messages)-1].Text()), StopReason: golm.StopEndTurn}, nil
}

func dialBuf(t *testing.T, h a2a.Handler) (*Client, func()) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	g := grpc.NewServer()
	RegisterServer(g, h)
	go func() { _ = g.Serve(lis) }()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	cli := NewClient(conn)
	return cli, func() { cli.Close(); g.Stop() }
}

func TestGRPCSendMessage(t *testing.T) {
	cli, cleanup := dialBuf(t, a2a.AgentHandler(&golm.Agent{Provider: stubProvider{}, Model: "x"}))
	defer cleanup()
	out, err := cli.SendMessage(context.Background(), "hi")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if out != "agent:hi" {
		t.Errorf("SendMessage = %q, want agent:hi", out)
	}
}

func TestGRPCStream(t *testing.T) {
	cli, cleanup := dialBuf(t, a2a.AgentHandler(&golm.Agent{Provider: stubProvider{}, Model: "x"}))
	defer cleanup()
	var streamed string
	out, err := cli.SendMessageStream(context.Background(), "yo", func(s string) { streamed += s })
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if streamed != "agent:yo" || out != "agent:yo" {
		t.Errorf("streamed=%q out=%q", streamed, out)
	}
}

func TestGRPCAsTool(t *testing.T) {
	cli, cleanup := dialBuf(t, a2a.AgentHandler(&golm.Agent{Provider: stubProvider{}, Model: "x"}))
	defer cleanup()
	out, err := cli.AsTool("remote", "delegate").Execute(context.Background(), json.RawMessage(`{"task":"ping"}`))
	if err != nil {
		t.Fatalf("tool: %v", err)
	}
	if len(out) != 1 || out[0].(golm.Text).Text != "agent:ping" {
		t.Errorf("AsTool = %v, want agent:ping", out)
	}
}
