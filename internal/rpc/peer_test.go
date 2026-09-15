// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package rpc_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leelsey/golm/internal/rpc"
)

func pair(t *testing.T) (a, b *rpc.Peer) {
	t.Helper()
	ta, tb := rpc.NewPipe()
	a, b = rpc.NewPeer(ta), rpc.NewPeer(tb)
	return a, b
}

func serve(t *testing.T, p *rpc.Peer) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); _ = p.Serve(context.Background()) }()
	t.Cleanup(func() {
		p.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("Serve did not return")
		}
	})
}

// The property Peer exists for.
func TestPeerCallsBothWays(t *testing.T) {
	a, b := pair(t)

	b.Handle("ask", func(ctx context.Context, _ json.RawMessage) (any, error) {
		var answer string
		if err := b.Call(ctx, "callback", map[string]string{"q": "may i"}, &answer); err != nil {
			return nil, err
		}
		return map[string]string{"got": answer}, nil
	})
	a.Handle("callback", func(_ context.Context, params json.RawMessage) (any, error) {
		var in struct{ Q string }
		if err := json.Unmarshal(params, &in); err != nil {
			return nil, err
		}
		return "yes:" + in.Q, nil
	})
	serve(t, a)
	serve(t, b)

	var out struct{ Got string }
	if err := a.Call(context.Background(), "ask", nil, &out); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out.Got != "yes:may i" {
		t.Errorf("got %q", out.Got)
	}
}

func TestPeerNotificationsHaveNoReply(t *testing.T) {
	a, b := pair(t)
	got := make(chan string, 1)
	b.Handle("note", func(_ context.Context, params json.RawMessage) (any, error) {
		var in struct{ Text string }
		_ = json.Unmarshal(params, &in)
		got <- in.Text
		return nil, nil
	})
	serve(t, a)
	serve(t, b)

	if err := a.Notify(context.Background(), "note", map[string]string{"text": "hello"}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	select {
	case v := <-got:
		if v != "hello" {
			t.Errorf("got %q", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the notification never arrived")
	}
}

func TestPeerUnknownMethod(t *testing.T) {
	a, b := pair(t)
	serve(t, a)
	serve(t, b)
	err := a.Call(context.Background(), "nosuch", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "no such method") {
		t.Errorf("err = %v", err)
	}

	if err := a.Notify(context.Background(), "nosuch", nil); err != nil {
		t.Errorf("Notify: %v", err)
	}
}

func TestPeerHandlerError(t *testing.T) {
	a, b := pair(t)
	b.Handle("boom", func(context.Context, json.RawMessage) (any, error) {
		return nil, errors.New("it went wrong")
	})
	serve(t, a)
	serve(t, b)
	err := a.Call(context.Background(), "boom", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "it went wrong") {
		t.Errorf("err = %v", err)
	}
}

// A handler that panics must not take the connection down.
func TestPeerSurvivesAPanickingHandler(t *testing.T) {
	a, b := pair(t)
	b.Handle("explode", func(context.Context, json.RawMessage) (any, error) {
		panic("handler exploded")
	})
	b.Handle("fine", func(context.Context, json.RawMessage) (any, error) { return "ok", nil })
	serve(t, a)
	serve(t, b)

	if err := a.Call(context.Background(), "explode", nil, nil); err == nil {
		t.Error("a panicking handler reported success")
	}
	var out string
	if err := a.Call(context.Background(), "fine", nil, &out); err != nil || out != "ok" {
		t.Errorf("the connection did not survive: %v / %q", err, out)
	}
}

func TestPeerCallHonoursContext(t *testing.T) {
	a, b := pair(t)
	release := make(chan struct{})
	b.Handle("slow", func(context.Context, json.RawMessage) (any, error) {
		<-release
		return "late", nil
	})
	serve(t, a)
	serve(t, b)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := a.Call(ctx, "slow", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the deadline", err)
	}
	close(release)
}

// Everyone waiting on a reply must be woken when the connection ends, or they wait for ever.
func TestPeerClosedConnectionWakesCallers(t *testing.T) {
	a, b := pair(t)
	b.Handle("slow", func(ctx context.Context, _ json.RawMessage) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	serve(t, b)
	done := make(chan struct{})
	go func() { defer close(done); _ = a.Serve(context.Background()) }()

	errc := make(chan error, 1)
	go func() { errc <- a.Call(context.Background(), "slow", nil, nil) }()
	time.Sleep(50 * time.Millisecond)
	a.Close()
	select {
	case err := <-errc:
		if err == nil {
			t.Error("a call outlived the connection and reported success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("closing the connection did not wake the caller")
	}
	<-done
	if err := a.Call(context.Background(), "slow", nil, nil); !errors.Is(err, rpc.ErrClosed) {
		t.Errorf("a call on a closed peer = %v", err)
	}
}

func TestPeerConcurrentCalls(t *testing.T) {
	a, b := pair(t)
	b.Handle("echo", func(_ context.Context, params json.RawMessage) (any, error) {
		var in struct{ N int }
		_ = json.Unmarshal(params, &in)
		return in.N, nil
	})
	serve(t, a)
	serve(t, b)

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var got int
			if err := a.Call(context.Background(), "echo", map[string]int{"n": i}, &got); err != nil {
				t.Errorf("call %d: %v", i, err)
				return
			}
			if got != i {
				t.Errorf("call %d got %d — replies were mismatched", i, got)
			}
		}(i)
	}
	wg.Wait()
}

// The far side is another program and can send anything.
func TestPeerSurvivesHostileMessages(t *testing.T) {
	ta, tb := rpc.NewPipe()
	p, other := rpc.NewPeer(ta), rpc.NewPeer(tb)
	p.Handle("ping", func(context.Context, json.RawMessage) (any, error) { return "pong", nil })
	serve(t, p)
	serve(t, other)

	for _, msg := range []string{
		`not json`,
		`null`,
		`[]`,
		`{}`,
		`{"jsonrpc":"2.0"}`,
		`{"jsonrpc":"2.0","id":1}`,
		`{"jsonrpc":"2.0","id":"unknown","result":null}`,
		`{"jsonrpc":"2.0","method":123}`,
		`{"jsonrpc":"2.0","id":{},"method":"ping"}`,
		`{"jsonrpc":"2.0","id":[1,2],"method":"nosuch"}`,
		`{"jsonrpc":"2.0","method":"ping","params":"not an object"}`,
		`{"jsonrpc":"2.0","method":"ping","params":null,"id":9,"extra":{"deep":[1,2,3]}}`,
		strings.Repeat(" ", 1000),
	} {
		if err := tb.WriteMessage([]byte(msg)); err != nil {
			t.Fatalf("write %q: %v", msg, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out string
	if err := other.Call(ctx, "ping", nil, &out); err != nil || out != "pong" {
		t.Errorf("a peer stopped working: %v / %q", err, out)
	}
}
