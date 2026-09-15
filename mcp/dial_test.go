// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

//go:build unix

package mcp

import (
	"context"
	"syscall"
	"testing"
	"time"
)

func TestDialCtxCancelDoesNotKillSubprocess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c, err := Dial(ctx, "cat")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	cancel()
	time.Sleep(100 * time.Millisecond)
	if err := c.cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("subprocess died when the dial ctx was cancelled: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
