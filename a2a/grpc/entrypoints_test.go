// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package grpc

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/a2a"
)

// Serve and Dial are the entry points a program outside this repository actually calls.
func TestServeAndDialOverRealTCP(t *testing.T) {
	addr := freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() {
		served <- Serve(ctx, addr, a2a.AgentHandler(&golm.Agent{Provider: stubProvider{}, Model: "x"}))
	}()

	cli := dialUntilReady(t, addr)
	defer cli.Close()

	out, err := cli.SendMessage(context.Background(), "hi")
	if err != nil {
		t.Fatalf("SendMessage over a real connection: %v", err)
	}
	if out != "agent:hi" {
		t.Errorf("SendMessage = %q, want agent:hi", out)
	}

	cancel()
	select {
	case err := <-served:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Serve returned %v, want nil or context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after its context was cancelled")
	}
}

func TestServeReportsAnUnusableAddress(t *testing.T) {
	err := Serve(context.Background(), "127.0.0.1:1", a2a.AgentHandler(&golm.Agent{Provider: stubProvider{}, Model: "x"}))
	if err == nil {
		t.Fatal("Serve on a privileged port reported success")
	}
}

func TestDialRejectsAnUnusableTarget(t *testing.T) {
	if c, err := Dial("\x00://nowhere"); err == nil {
		_ = c.Close()
		t.Fatal("Dial accepted a malformed target")
	}
}

// TaskStore is how retention is tuned before serving.
func TestServerTaskStoreIsTheOneInUse(t *testing.T) {
	g := grpc.NewServer()
	srv := RegisterServer(g, a2a.AgentHandler(&golm.Agent{Provider: stubProvider{}, Model: "x"}))
	st := srv.TaskStore()
	if st == nil {
		t.Fatal("TaskStore() is nil; retention cannot be tuned")
	}
	st.MaxTasks = 7
	if srv.TaskStore().MaxTasks != 7 {
		t.Error("TaskStore() hands back a copy; tuning it changes nothing")
	}
	g.Stop()
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func dialUntilReady(t *testing.T, addr string) *Client {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		cli, err := Dial(addr)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			_, serr := cli.SendMessage(ctx, "ping")
			cancel()
			if serr == nil {
				return cli
			}
			_ = cli.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never became reachable at %s", addr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
