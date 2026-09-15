// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestClientCloseUnblocksReadLoop(t *testing.T) {
	cEnd, _ := NewPipe()
	before := runtime.NumGoroutine()
	c := NewClient(cEnd)
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before {
		t.Errorf("readLoop goroutine leaked after Close: before=%d after=%d", before, n)
	}
}

type blockTransport struct{}

func (blockTransport) ReadMessage() ([]byte, error) { select {} }
func (blockTransport) WriteMessage([]byte) error    { select {} }
func (blockTransport) Close() error                 { return nil }

func TestCallHonoursCtxDuringBlockedWrite(t *testing.T) {
	c := NewClient(blockTransport{})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Call(ctx, "x", nil, nil) }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("expected a ctx error during a blocked write")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Call did not honour ctx during a blocked write")
	}
}

func TestServerConcurrentDispatch(t *testing.T) {
	cEnd, sEnd := NewPipe()
	srv := NewServer()
	release := make(chan struct{})
	srv.Handle("slow", func(context.Context, json.RawMessage) (any, error) { <-release; return "done", nil })
	srv.Handle("ping", func(context.Context, json.RawMessage) (any, error) { return "pong", nil })
	go func() { _ = srv.Serve(context.Background(), sEnd) }()

	c := NewClient(cEnd)
	defer c.Close()
	go func() { _ = c.Call(context.Background(), "slow", nil, nil) }()
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var out string
	if err := c.Call(ctx, "ping", nil, &out); err != nil || out != "pong" {
		t.Fatalf("ping blocked by slow handler: out=%q err=%v", out, err)
	}
	close(release)
}

func TestServeRejectsBadVersion(t *testing.T) {
	var out bytes.Buffer
	tr := NewStreamTransport(strings.NewReader(`{"jsonrpc":"1.0","id":1,"method":"x"}`+"\n"), &out, nil)
	_ = NewServer().Serve(context.Background(), tr)
	if !bytes.Contains(out.Bytes(), []byte("-32600")) {
		t.Errorf("bad jsonrpc version not rejected: %s", out.String())
	}
}
