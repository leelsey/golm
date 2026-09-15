// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"testing"
	"time"
)

func TestClientCloseKillsHungChild(t *testing.T) {
	c, err := Dial(context.Background(), "sleep", "30")
	if err != nil {
		t.Skipf("sleep unavailable: %v", err)
	}
	c.CloseGrace = 50 * time.Millisecond
	done := make(chan struct{})
	go func() { _ = c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close hung on a child that ignores stdin EOF")
	}
}

// A server that spawns a helper leaves it holding the write end of stderr.
func TestClientCloseSurvivesAForkingServer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no shell to fork with")
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	d := Dialer{Stderr: w, CloseGrace: 100 * time.Millisecond}

	c, err := d.Dial(context.Background(), "sh", "-c", "sleep 30 & exit 0")
	if err != nil {
		w.Close()
		t.Skipf("sh unavailable: %v", err)
	}
	w.Close()

	done := make(chan struct{})
	go func() { _ = c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close hung on a pipe held open by a grandchild")
	}

	if err := r.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(r); err != nil {
		t.Errorf("the grandchild outlived Close: %v", err)
	}
}

// A host that reconnects a dead server does so many times.
func TestRepeatedDialAndCloseLeavesNothingBehind(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no shell to fork with")
	}
	settle := func() int {
		for i := 0; i < 40; i++ {
			runtime.GC()
			time.Sleep(10 * time.Millisecond)
		}
		return runtime.NumGoroutine()
	}
	before := settle()

	for i := 0; i < 15; i++ {
		d := Dialer{Stderr: io.Discard, CloseGrace: 50 * time.Millisecond}
		c, err := d.Dial(context.Background(), "sh", "-c", "sleep 30 & sleep 30")
		if err != nil {
			t.Skipf("sh unavailable: %v", err)
		}
		if err := c.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			t.Logf("round %d close: %v", i, err)
		}
	}

	after := settle()
	if after > before+8 {
		buf := make([]byte, 1<<16)
		n := runtime.Stack(buf, true)
		t.Fatalf("goroutines %d -> %d after 15 dial/close cycles\n%s", before, after, buf[:n])
	}
}

// Dialer.CloseGrace sets the subprocess's WaitDelay AND the Client's own wait.
func TestDialerCloseGraceReachesTheClient(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no shell to fork with")
	}
	d := Dialer{Stderr: io.Discard, CloseGrace: 50 * time.Millisecond}
	c, err := d.Dial(context.Background(), "sh", "-c", "sleep 30")
	if err != nil {
		t.Skipf("sh unavailable: %v", err)
	}
	if c.CloseGrace != d.CloseGrace {
		t.Errorf("Client.CloseGrace = %v, want the dialer's %v", c.CloseGrace, d.CloseGrace)
	}
	start := time.Now()
	_ = c.Close()
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("Close took %v; the dialer's grace was %v", took, d.CloseGrace)
	}
}
