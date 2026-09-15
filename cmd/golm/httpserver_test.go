// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/a2a"
	"github.com/leelsey/golm/netbus"
	"github.com/leelsey/golm/openaiapi"
)

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func waitServing(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server never came up at %s", addr)
}

// The plainest thing a served surface must do.
func TestHTTPServerServesThenStopsOnCancel(t *testing.T) {
	addr := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- runHTTPServer(ctx, addr, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, "ok")
		}))
	}()
	waitServing(t, addr)

	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "ok" {
		t.Errorf("body = %q", b)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("a clean shutdown returned %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("runHTTPServer did not return after its context was cancelled")
	}

	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("the address is still held after shutdown: %v", err)
	}
	_ = l.Close()
}

// A request already in flight is drained rather than cut off.
func TestHTTPServerDrainsAnInFlightRequest(t *testing.T) {
	addr := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})

	done := make(chan error, 1)
	go func() {
		done <- runHTTPServer(ctx, addr, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(started)
			time.Sleep(300 * time.Millisecond)
			fmt.Fprint(w, "finished")
		}))
	}()
	waitServing(t, addr)

	type result struct {
		body string
		err  error
	}
	res := make(chan result, 1)
	go func() {
		resp, err := http.Get("http://" + addr + "/slow")
		if err != nil {
			res <- result{err: err}
			return
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		res <- result{body: string(b)}
	}()

	<-started
	cancel()

	select {
	case r := <-res:
		if r.err != nil {
			t.Fatalf("an in-flight request was cut off: %v", r.err)
		}
		if r.body != "finished" {
			t.Errorf("body = %q, want the handler's full answer", r.body)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the in-flight request never completed")
	}
	if err := <-done; err != nil {
		t.Errorf("shutdown returned %v", err)
	}
}

// The shutdown hooks are how a long-lived connection is released.
func TestHTTPServerRunsItsShutdownHooks(t *testing.T) {
	addr := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())

	var ran atomic.Int64
	hookDone := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runHTTPServer(ctx, addr,
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "ok") }),
			func() { ran.Add(1); close(hookDone) })
	}()
	waitServing(t, addr)
	cancel()

	select {
	case <-hookDone:
	case <-time.After(15 * time.Second):
		t.Fatal("the shutdown hook never ran; a long-lived connection would hold the server open")
	}
	if err := <-done; err != nil {
		t.Errorf("shutdown returned %v", err)
	}
	if n := ran.Load(); n != 1 {
		t.Errorf("the hook ran %d times, want 1", n)
	}
}

// A bind failure must be REPORTED, not swallowed into a server that silently never listens.
func TestHTTPServerReportsABindFailure(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()

	done := make(chan error, 1)
	go func() {
		done <- runHTTPServer(context.Background(), l.Addr().String(), http.NotFoundHandler())
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("binding an address already in use reported success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a bind failure was never reported; the server hangs instead")
	}
}

// A context that is already over must not leave a listener bound.
func TestHTTPServerWithAnAlreadyCancelledContext(t *testing.T) {
	addr := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { done <- runHTTPServer(ctx, addr, http.NotFoundHandler()) }()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("returned %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("runHTTPServer hung on a context that was already done")
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("the address is still held: %v", err)
	}
	_ = l.Close()
}

// The composition `golm bus` actually runs.
func TestBusShutsDownPromptlyWithALiveSubscriber(t *testing.T) {
	hub := netbus.NewHub()
	addr := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- runHTTPServer(ctx, addr, hub.Handler(), hub.CloseAll) }()
	waitServing(t, addr)

	subCtx, subCancel := context.WithCancel(context.Background())
	defer subCancel()
	req, err := http.NewRequestWithContext(subCtx, http.MethodGet, "http://"+addr+"/subscribe", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer resp.Body.Close()
	reading := make(chan struct{})
	go func() { defer close(reading); _, _ = io.Copy(io.Discard, resp.Body) }()

	time.Sleep(100 * time.Millisecond)

	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("shutdown returned %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the bus never shut down with a subscriber attached")
	}

	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("shutdown took %v — the subscriber was not released, it timed out", took)
	}
	select {
	case <-reading:
	case <-time.After(5 * time.Second):
		t.Error("the subscriber's stream was never closed")
	}
}

type blockedHandler struct{ release chan struct{} }

func (h blockedHandler) HandleMessage(ctx context.Context, _ a2a.Message) ([]a2a.Part, error) {
	select {
	case <-h.release:
		return []a2a.Part{a2a.TextPart("done")}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// a2a-serve must drain its task store on the way down, the same way the gRPC binding's Serve does.
func TestA2AServeDrainsItsTasksOnShutdown(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	srv := a2a.NewServer(a2a.AgentCard{Name: "probe"}, blockedHandler{release: release})

	addr := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	drain := func() {
		sctx, c := context.WithTimeout(context.Background(), a2aDrainTimeout)
		defer c()
		srv.TaskStore().Shutdown(sctx)
	}
	done := make(chan error, 1)
	go func() { done <- runHTTPServer(ctx, addr, srv.HTTPHandler(), drain) }()
	waitServing(t, addr)

	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "message/stream",
		"params": map[string]any{"message": map[string]any{
			"role": "user", "parts": []map[string]any{{"kind": "text", "text": "hi"}}}},
	})
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("message/stream: %v", err)
	}
	defer resp.Body.Close()
	streamRead := make(chan struct{})
	go func() { defer close(streamRead); _, _ = io.Copy(io.Discard, resp.Body) }()
	time.Sleep(200 * time.Millisecond)

	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("shutdown returned %v", err)
		}
	case <-time.After(25 * time.Second):
		t.Fatal("a2a-serve never shut down with a live stream")
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("shutdown took %v — the task store was not drained, Shutdown timed out", took)
	}
	select {
	case <-streamRead:
	case <-time.After(5 * time.Second):
		t.Error("the stream was never closed")
	}
}

type blockingRunner struct{ release chan struct{} }

func (r blockingRunner) StreamMessage(ctx context.Context, _ *golm.Session, _ golm.Message,
	fn func(golm.StreamEvent) error) (golm.Result, error) {
	_ = fn(golm.StreamEvent{Type: golm.EventTextDelta, Text: "start"})
	select {
	case <-r.release:
	case <-ctx.Done():
		return golm.Result{}, ctx.Err()
	}
	return golm.Result{Message: golm.AssistantText("done"), StopReason: golm.StopEndTurn}, nil
}

// The second phase, on the surface that has no drain hook of its own.
func TestServeStopsWithoutWaitingOutTheWholeBudget(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	srv := openaiapi.NewServer()
	srv.Add("m", blockingRunner{release: release}, "probe")

	addr := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runHTTPServer(ctx, addr, srv.Handler()) }()
	waitServing(t, addr)

	body := `{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("completion: %v", err)
	}
	defer resp.Body.Close()
	read := make(chan struct{})
	go func() { defer close(read); _, _ = io.Copy(io.Discard, resp.Body) }()
	time.Sleep(200 * time.Millisecond)

	start := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(25 * time.Second):
		t.Fatal("serve never shut down with a live completion")
	}
	took := time.Since(start)
	if took >= shutdownBudget {
		t.Errorf("shutdown took %v — it waited out the whole budget instead of cancelling", took)
	}
	if took < drainGrace {
		t.Errorf("shutdown took %v — it cut the request off before its grace period", took)
	}
	select {
	case <-read:
	case <-time.After(5 * time.Second):
		t.Error("the streaming response was never closed")
	}
}

// The grace is real: a request that finishes inside it is not cancelled.
func TestShortRequestsFinishInsideTheGrace(t *testing.T) {
	addr := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	var cancelled atomic.Bool

	done := make(chan error, 1)
	go func() {
		done <- runHTTPServer(ctx, addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(started)
			select {
			case <-time.After(300 * time.Millisecond):
			case <-r.Context().Done():
				cancelled.Store(true)
			}
			fmt.Fprint(w, "finished")
		}))
	}()
	waitServing(t, addr)

	res := make(chan string, 1)
	go func() {
		resp, err := http.Get("http://" + addr + "/slow")
		if err != nil {
			res <- "ERR:" + err.Error()
			return
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		res <- string(b)
	}()
	<-started
	cancel()

	if got := <-res; got != "finished" {
		t.Errorf("body = %q, want the handler's full answer", got)
	}
	if cancelled.Load() {
		t.Error("a request that fits inside the grace had its context cancelled")
	}
	<-done
}
