// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package a2a

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/leelsey/golm"
)

type echoHandler struct{}

func (echoHandler) HandleMessage(_ context.Context, msg Message) ([]Part, error) {
	return []Part{TextPart("reply:" + msg.Text())}, nil
}
func (echoHandler) HandleMessageStream(_ context.Context, msg Message, emit func(string) error) ([]Part, error) {
	out := "reply:" + msg.Text()
	if err := emit(out); err != nil {
		return nil, err
	}
	return []Part{TextPart(out)}, nil
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	card := CardForAgent("echo-agent", "echoes input", "http://example/")
	srv := NewServer(card, echoHandler{})
	hs := httptest.NewServer(srv.HTTPHandler())
	t.Cleanup(hs.Close)
	return hs
}

func TestAgentCardDiscovery(t *testing.T) {
	hs := newServer(t)
	c := NewClient(hs.URL)
	card, err := c.AgentCard(context.Background())
	if err != nil {
		t.Fatalf("card: %v", err)
	}
	if card.Name != "echo-agent" || !card.Capabilities.Streaming || len(card.Skills) != 1 {
		t.Errorf("card = %+v", card)
	}
}

func TestMessageSend(t *testing.T) {
	hs := newServer(t)
	c := NewClient(hs.URL)
	out, err := c.SendMessage(context.Background(), "hello")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if out != "reply:hello" {
		t.Errorf("send result = %q", out)
	}
}

func TestMessageStream(t *testing.T) {
	hs := newServer(t)
	c := NewClient(hs.URL)
	var streamed string
	out, err := c.SendMessageStream(context.Background(), "stream", func(s string) { streamed += s })
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if streamed != "reply:stream" || out != "reply:stream" {
		t.Errorf("streamed=%q out=%q", streamed, out)
	}
}

func TestPartUnionRoundTrip(t *testing.T) {
	parts := []Part{
		TextPart("hi"),
		{File: &FilePart{Name: "a.txt", MimeType: "text/plain", URI: "http://x/a"}},
		{Data: json.RawMessage(`{"k":1}`)},
	}
	b, err := json.Marshal(parts)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out []Part
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out[0].Text != "hi" || out[1].File == nil || out[1].File.Name != "a.txt" || string(out[2].Data) != `{"k":1}` {
		t.Errorf("round-trip lost data: %+v", out)
	}
}

type stubProvider struct{}

func (stubProvider) Name() string                    { return "stub" }
func (stubProvider) Capabilities() golm.Capabilities { return golm.Capabilities{} }
func (stubProvider) Complete(_ context.Context, req golm.Request) (golm.Response, error) {
	return golm.Response{Message: golm.AssistantText("agent:" + req.Messages[len(req.Messages)-1].Text()), StopReason: golm.StopEndTurn}, nil
}
func (s stubProvider) Stream(ctx context.Context, req golm.Request, fn func(golm.StreamEvent) error) (golm.Response, error) {
	return s.Complete(ctx, req)
}

func TestAgentBridgeAndAsTool(t *testing.T) {
	agent := &golm.Agent{Provider: stubProvider{}, Model: "x"}
	card := CardForAgent("bridged", "wraps a golm agent", "http://example/")
	srv := NewServer(card, AgentHandler(agent))
	hs := httptest.NewServer(srv.HTTPHandler())
	t.Cleanup(hs.Close)

	tool := NewClient(hs.URL).AsTool("remote", "delegate to remote agent")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"task":"ping"}`))
	if err != nil {
		t.Fatalf("tool execute: %v", err)
	}
	if len(out) != 1 || out[0].(golm.Text).Text != "agent:ping" {
		t.Errorf("remote tool result = %v", out)
	}
}
