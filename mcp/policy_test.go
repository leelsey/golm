// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/leelsey/golm"
)

func countingTool(ran *atomic.Int64, ctxSeen *atomic.Value) golm.Tool {
	return golm.NewTool("echo", "echoes its input", json.RawMessage(`{"type":"object"}`),
		func(ctx context.Context, in json.RawMessage) (string, error) {
			ran.Add(1)
			if ctxSeen != nil {
				if v := ctx.Value(policyKey{}); v != nil {
					ctxSeen.Store(v)
				}
			}
			return "echoed:" + string(in), nil
		})
}

type policyKey struct{}

func TestServerPolicyDeniesWithoutExecuting(t *testing.T) {
	var ran atomic.Int64
	srv := NewServer("test-server", "0.1")
	srv.AddTools(countingTool(&ran, nil))
	got := srv.WithPolicy(func(_ context.Context, req golm.ToolRequest) (context.Context, error) {
		if req.Call.Name != "echo" {
			t.Errorf("policy saw call name %q", req.Call.Name)
		}
		if req.Tool == nil || req.Tool.Name() != "echo" {
			t.Errorf("policy saw tool %v", req.Tool)
		}
		if req.Agent != "" || req.Step != 0 || req.Call.ID != "" {
			t.Errorf("fabricated agent context: %q %d %q", req.Agent, req.Step, req.Call.ID)
		}
		return nil, errors.New("no network today")
	})
	if got != srv {
		t.Fatalf("WithPolicy returned %p, want %p", got, srv)
	}
	client := newPair(t, srv)

	out, isErr, err := client.CallTool(context.Background(), "echo", json.RawMessage(`{"x":1}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !isErr {
		t.Errorf("denied call was not an error result: %q", out)
	}
	if !strings.Contains(out, "no network today") {
		t.Errorf("denial reason missing from %q", out)
	}
	if !strings.Contains(out, `"echo"`) {
		t.Errorf("tool name missing from %q", out)
	}
	if n := ran.Load(); n != 0 {
		t.Errorf("tool ran %d times under a denying policy", n)
	}
}

func TestServerPolicyPanicDenies(t *testing.T) {
	var ran atomic.Int64
	srv := NewServer("test-server", "0.1")
	srv.AddTools(countingTool(&ran, nil))
	srv.WithPolicy(func(context.Context, golm.ToolRequest) (context.Context, error) {
		panic("policy blew up")
	})
	client := newPair(t, srv)

	out, isErr, err := client.CallTool(context.Background(), "echo", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !isErr {
		t.Errorf("panicking policy allowed the call: %q", out)
	}
	if !strings.Contains(out, golm.ErrToolDenied.Error()) {
		t.Errorf("panic denial not wrapped as ErrToolDenied: %q", out)
	}
	if n := ran.Load(); n != 0 {
		t.Errorf("tool ran %d times after a panicking policy", n)
	}

	if _, _, err := client.CallTool(context.Background(), "echo", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("server died after policy panic: %v", err)
	}
}

func TestServerNoPolicyUnchanged(t *testing.T) {
	var ran atomic.Int64
	srv := NewServer("test-server", "0.1")
	srv.AddTools(countingTool(&ran, nil))
	client := newPair(t, srv)

	out, isErr, err := client.CallTool(context.Background(), "echo", json.RawMessage(`{"x":1}`))
	if err != nil || isErr {
		t.Fatalf("call: out=%q isErr=%v err=%v", out, isErr, err)
	}
	if out != `echoed:{"x":1}` {
		t.Errorf("call result = %q", out)
	}
	if n := ran.Load(); n != 1 {
		t.Errorf("tool ran %d times", n)
	}
}

func TestServerPolicyContextReachesTool(t *testing.T) {
	var ran atomic.Int64
	var seen atomic.Value
	srv := NewServer("test-server", "0.1")
	srv.AddTools(countingTool(&ran, &seen))
	srv.WithPolicy(func(ctx context.Context, _ golm.ToolRequest) (context.Context, error) {
		return context.WithValue(ctx, policyKey{}, "sandbox-42"), nil
	})
	client := newPair(t, srv)

	if _, isErr, err := client.CallTool(context.Background(), "echo", json.RawMessage(`{}`)); err != nil || isErr {
		t.Fatalf("call: isErr=%v err=%v", isErr, err)
	}
	if v, _ := seen.Load().(string); v != "sandbox-42" {
		t.Errorf("tool ran with context value %q, want %q", v, "sandbox-42")
	}
}

func TestServerPolicyNotAskedForUnknownTool(t *testing.T) {
	srv := NewServer("test-server", "0.1")
	var asked atomic.Int64
	srv.WithPolicy(func(ctx context.Context, _ golm.ToolRequest) (context.Context, error) {
		asked.Add(1)
		return ctx, nil
	})
	client := newPair(t, srv)

	if _, isErr, _ := client.CallTool(context.Background(), "nope", nil); !isErr {
		t.Errorf("expected isError for unknown tool")
	}
	if n := asked.Load(); n != 0 {
		t.Errorf("policy asked %d times about an unknown tool", n)
	}
}
