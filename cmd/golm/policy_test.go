// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/leelsey/golm"
)

func testApprover(in io.Reader) *approver {
	return &approver{
		gate: make(chan struct{}, 1), in: bufio.NewReader(in), out: io.Discard,
		always: map[string]bool{},
	}
}

func call(name string) golm.ToolRequest {
	return golm.ToolRequest{Call: golm.ToolUse{ID: "c1", Name: name, Input: json.RawMessage(`{}`)}}
}

// One approver backs every agent.
func TestApproverQueueHonoursContext(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { r.Close(); w.Close() }()
	a := testApprover(r)

	first := make(chan struct{})
	go func() { defer close(first); _, _ = a.Approve(context.Background(), call("run")) }()
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := a.Approve(ctx, call("write_file")); done <- err }()
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("queued call returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a cancelled run could not leave the approval queue")
	}
	io.WriteString(w, "n\n")
	<-first
}

// An abandoned prompt's reader outlives it, blocked in the kernel.
func TestApproverAbandonedReadDoesNotOverlapTheNext(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { r.Close(); w.Close() }()
	a := testApprover(r)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	if _, err := a.Approve(ctx, call("run")); !errors.Is(err, context.Canceled) {
		t.Fatalf("first call: %v, want context.Canceled", err)
	}

	var wg sync.WaitGroup
	got := make(chan bool, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		ok, _ := a.Approve(context.Background(), call("run"))
		got <- ok
	}()

	io.WriteString(w, "y\n")
	time.Sleep(50 * time.Millisecond)
	io.WriteString(w, "y\n")

	select {
	case ok := <-got:
		if !ok {
			t.Error("the answer did not reach the waiting prompt")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the second prompt never got an answer")
	}
	wg.Wait()
}

// "always" answered on one prompt must satisfy the run queued behind it.
func TestApproverAlwaysIsRecheckedAfterQueueing(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { r.Close(); w.Close() }()
	a := testApprover(r)

	first := make(chan bool, 1)
	go func() { ok, _ := a.Approve(context.Background(), call("run")); first <- ok }()
	time.Sleep(50 * time.Millisecond)

	second := make(chan bool, 1)
	go func() { ok, _ := a.Approve(context.Background(), call("run")); second <- ok }()
	time.Sleep(50 * time.Millisecond)

	io.WriteString(w, "a\n")
	for i, ch := range []chan bool{first, second} {
		select {
		case ok := <-ch:
			if !ok {
				t.Errorf("call %d was refused", i)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("call %d was asked again despite \"always\"", i)
		}
	}
}
