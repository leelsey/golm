// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package rpc_test

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"
	"time"

	"github.com/leelsey/golm/internal/rpc"
)

// Every connection opened and closed must leave nothing running.
func TestPeerLeavesNoGoroutinesBehind(t *testing.T) {
	settle := func() int {
		for i := 0; i < 50; i++ {
			runtime.GC()
			time.Sleep(10 * time.Millisecond)
		}
		return runtime.NumGoroutine()
	}
	before := settle()

	for i := 0; i < 50; i++ {
		ta, tb := rpc.NewPipe()
		p, other := rpc.NewPeer(ta), rpc.NewPeer(tb)
		p.Handle("ping", func(context.Context, json.RawMessage) (any, error) { return "pong", nil })
		pd, od := make(chan struct{}), make(chan struct{})
		go func() { defer close(pd); _ = p.Serve(context.Background()) }()
		go func() { defer close(od); _ = other.Serve(context.Background()) }()

		var out string
		if err := other.Call(context.Background(), "ping", nil, &out); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		if err := other.Notify(context.Background(), "ping", nil); err != nil {
			t.Fatalf("round %d notify: %v", i, err)
		}
		p.Close()
		other.Close()
		<-pd
		<-od
	}

	after := settle()
	if after > before+5 {
		buf := make([]byte, 1<<16)
		n := runtime.Stack(buf, true)
		t.Fatalf("goroutines %d -> %d after 50 connections\n%s", before, after, buf[:n])
	}
}
