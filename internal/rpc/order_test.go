// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package rpc_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leelsey/golm/internal/rpc"
)

// A stream of notifications is ordered by the sender.
func TestNotificationsAreProcessedInOrder(t *testing.T) {
	ta, tb := rpc.NewPipe()
	p, other := rpc.NewPeer(ta), rpc.NewPeer(tb)

	var mu sync.Mutex
	var got []int
	p.Handle("chunk", func(_ context.Context, params json.RawMessage) (any, error) {
		var in struct{ N int }
		if err := json.Unmarshal(params, &in); err != nil {
			return nil, err
		}

		if in.N%3 == 0 {
			time.Sleep(2 * time.Millisecond)
		}
		mu.Lock()
		got = append(got, in.N)
		mu.Unlock()
		return nil, nil
	})
	serve(t, p)
	serve(t, other)

	const n = 60
	for i := 0; i < n; i++ {
		if err := other.Notify(context.Background(), "chunk", map[string]int{"n": i}); err != nil {
			t.Fatalf("notify %d: %v", i, err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		done := len(got) == n
		mu.Unlock()
		if done || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != n {
		t.Fatalf("%d of %d notifications arrived", len(got), n)
	}
	for i, v := range got {
		if v != i {
			t.Fatalf("notification %d was processed at position %d: %v", v, i, fmt.Sprint(got[:min(len(got), 12)]))
		}
	}
}

// Server has the same read loop and the same obligation.
func TestServerNotificationsAreProcessedInOrder(t *testing.T) {
	cEnd, sEnd := rpc.NewPipe()
	srv := rpc.NewServer()
	var mu sync.Mutex
	var got []int
	srv.Handle("chunk", func(_ context.Context, params json.RawMessage) (any, error) {
		var in struct{ N int }
		if err := json.Unmarshal(params, &in); err != nil {
			return nil, err
		}
		if in.N%3 == 0 {
			time.Sleep(2 * time.Millisecond)
		}
		mu.Lock()
		got = append(got, in.N)
		mu.Unlock()
		return nil, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx, sEnd) }()

	const n = 60
	for i := 0; i < n; i++ {
		msg := fmt.Sprintf(`{"jsonrpc":"2.0","method":"chunk","params":{"n":%d}}`, i)
		if err := cEnd.WriteMessage([]byte(msg)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		done := len(got) == n
		mu.Unlock()
		if done || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != n {
		t.Fatalf("%d of %d notifications arrived", len(got), n)
	}
	for i, v := range got {
		if v != i {
			t.Fatalf("notification %d was processed at position %d: %v", v, i, got[:min(len(got), 12)])
		}
	}
}

// Notifications must not starve request dispatch.
func TestNotificationsDoNotStarveRequests(t *testing.T) {
	ta, tb := rpc.NewPipe()
	p, other := rpc.NewPeer(ta), rpc.NewPeer(tb)

	block := make(chan struct{})
	p.Handle("slow", func(ctx context.Context, _ json.RawMessage) (any, error) {
		select {
		case <-block:
		case <-ctx.Done():
		}
		return nil, nil
	})
	p.Handle("ping", func(context.Context, json.RawMessage) (any, error) { return "pong", nil })
	serve(t, p)
	serve(t, other)
	defer close(block)

	for i := 0; i < 80; i++ {
		if err := other.Notify(context.Background(), "slow", nil); err != nil {
			t.Fatalf("notify %d: %v", i, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var out string
	if err := other.Call(ctx, "ping", nil, &out); err != nil {
		t.Fatalf("a request could not be served behind queued notifications: %v", err)
	}
	if out != "pong" {
		t.Errorf("got %q", out)
	}
}

// Server has the same two budgets for the same reason.
func TestServerNotificationsDoNotStarveRequests(t *testing.T) {
	cEnd, sEnd := rpc.NewPipe()
	srv := rpc.NewServer()
	block := make(chan struct{})
	srv.Handle("slow", func(ctx context.Context, _ json.RawMessage) (any, error) {
		select {
		case <-block:
		case <-ctx.Done():
		}
		return nil, nil
	})
	srv.Handle("ping", func(context.Context, json.RawMessage) (any, error) { return "pong", nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx, sEnd) }()
	defer close(block)

	for i := 0; i < 80; i++ {
		if err := cEnd.WriteMessage([]byte(`{"jsonrpc":"2.0","method":"slow"}`)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if err := cEnd.WriteMessage([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); err != nil {
		t.Fatal(err)
	}

	got := make(chan []byte, 1)
	go func() {
		msg, err := cEnd.ReadMessage()
		if err == nil {
			got <- msg
		}
	}()
	select {
	case msg := <-got:
		if !strings.Contains(string(msg), "pong") {
			t.Errorf("reply = %s", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a request could not be served behind queued notifications")
	}
}
