// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leelsey/golm"
)

// A stdio server that exits leaves a client whose every later call fails identically for the rest of the process.
func TestRemoteReDialsAfterTheServerDies(t *testing.T) {
	cache := t.TempDir()
	marker := filepath.Join(t.TempDir(), "starts")
	r := newRemote(t, cache, marker)
	defer r.Close()

	tools, err := r.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(tools))
	}
	call := func() (string, error) {
		out, err := tools[0].Execute(context.Background(), json.RawMessage(`{"input":"x"}`))
		if err != nil {
			return "", err
		}
		var sb strings.Builder
		for _, c := range out {
			if txt, ok := c.(golm.Text); ok {
				sb.WriteString(txt.Text)
			}
		}
		return sb.String(), nil
	}
	if _, err := call(); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if !r.Connected() {
		t.Fatal("the first call should have started the server")
	}
	before := starts(t, marker)

	r.mu.Lock()
	dead := r.client
	r.mu.Unlock()
	if err := dead.Close(); err != nil && !strings.Contains(err.Error(), "file already closed") {
		t.Fatalf("close: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for dead.Live() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if dead.Live() {
		t.Fatal("the client never noticed its transport ended")
	}

	got, err := call()
	if err != nil {
		t.Fatalf("call after the server died: %v — a Remote must re-dial", err)
	}
	if !strings.Contains(got, "ran alpha") {
		t.Errorf("result = %q, want the replacement server's answer", got)
	}
	if after := starts(t, marker); after != before+1 {
		t.Errorf("server starts = %d, want one more than %d", after, before)
	}
}

// Close is deliberate. A tool call already in flight must not resurrect what the caller just shut down.
func TestRemoteRefusesToReconnectAfterClose(t *testing.T) {
	cache := t.TempDir()
	marker := filepath.Join(t.TempDir(), "starts")
	r := newRemote(t, cache, marker)

	tools, err := r.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	before := starts(t, marker)
	if _, err := tools[0].Execute(context.Background(), json.RawMessage(`{"input":"x"}`)); err == nil {
		t.Fatal("a call after Close started a server the caller cannot shut down")
	} else if !strings.Contains(err.Error(), "closed") {
		t.Errorf("err = %v, want one saying the remote is closed", err)
	}
	if after := starts(t, marker); after != before {
		t.Errorf("Close was followed by %d further server start(s)", after-before)
	}
}

// The server's own diagnostics are usually the only account of why it refused.
func TestRemoteKeepsServerStderrOutOfTheTerminal(t *testing.T) {
	r := &Remote{Name: "nope", Command: "/nonexistent/golm-mcp-server-probe"}
	defer r.Close()
	_, err := r.Tools(context.Background())
	if err == nil {
		t.Fatal("dialling a command that does not exist should fail")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("err = %v, should name the server", err)
	}
}
