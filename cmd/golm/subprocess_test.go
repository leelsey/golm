// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leelsey/golm/acp"
)

var (
	buildOnce sync.Once
	binPath   string
	buildErr  error
)

func golmBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "golm-bin")
		if err != nil {
			buildErr = err
			return
		}
		binPath = filepath.Join(dir, "golm")
		out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("build: %v: %s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatalf("%v", buildErr)
	}
	return binPath
}

func catConfig(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "golm.json")
	body := `{
      "default_agent": "echo",
      "providers": [{"name":"cat","type":"cli","command":"/bin/cat","prompt_via":"stdin"}],
      "agents": [{"name":"echo","provider":"cat","model":"cat","description":"Echoes."}]
    }`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

type rpcProc struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Reader
	stderr *strings.Builder
	mu     sync.Mutex
}

func startRPC(t *testing.T, args ...string) *rpcProc {
	t.Helper()
	cmd := exec.Command(golmBinary(t), args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	p := &rpcProc{cmd: cmd, in: stdin, out: bufio.NewReader(stdout), stderr: &strings.Builder{}}
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		sc := bufio.NewScanner(errPipe)
		for sc.Scan() {
			p.mu.Lock()
			p.stderr.WriteString(sc.Text() + "\n")
			p.mu.Unlock()
		}
	}()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return p
}

func (p *rpcProc) call(t *testing.T, req any) map[string]any {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.in.Write(append(b, '\n')); err != nil {
		t.Fatalf("write: %v (stderr: %s)", err, p.errText())
	}
	type res struct {
		line string
		err  error
	}
	ch := make(chan res, 1)
	go func() {
		line, err := p.out.ReadString('\n')
		ch <- res{line, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil && strings.TrimSpace(r.line) == "" {
			t.Fatalf("read: %v (stderr: %s)", r.err, p.errText())
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(r.line)), &out); err != nil {
			t.Fatalf("stdout was not one JSON message: %v\n  line: %q\n  stderr: %s",
				err, r.line, p.errText())
		}
		return out
	case <-time.After(20 * time.Second):
		t.Fatalf("no reply (stderr: %s)", p.errText())
		return nil
	}
}

func (p *rpcProc) errText() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stderr.String()
}

// `golm mcp-server` over a real pipe.
func TestMCPServerSubprocessSpeaksMCP(t *testing.T) {
	p := startRPC(t, "mcp-server", "--config", catConfig(t))

	init := p.call(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{},
			"clientInfo": map[string]any{"name": "probe", "version": "1"}},
	})
	result, ok := init["result"].(map[string]any)
	if !ok {
		t.Fatalf("initialize returned %v", init)
	}
	if result["protocolVersion"] != "2025-11-25" {
		t.Errorf("protocolVersion = %v", result["protocolVersion"])
	}

	list := p.call(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{}})
	tools, _ := list["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools/list returned %d tools: %v", len(tools), list)
	}
	if name := tools[0].(map[string]any)["name"]; name != "echo" {
		t.Errorf("tool name = %v", name)
	}

	call := p.call(t, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "echo", "arguments": map[string]any{"input": "over a pipe"}}})
	res, _ := call["result"].(map[string]any)
	if res == nil || res["isError"] == true {
		t.Fatalf("tools/call failed: %v (stderr: %s)", call, p.errText())
	}

	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(p.errText(), "mcp-server") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if e := p.errText(); !strings.Contains(e, "mcp-server") {
		t.Errorf("the server said nothing on stderr: %q", e)
	}

	_ = p.in.Close()
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("exit after stdin closed: %v (stderr: %s)", err, p.errText())
		}
	case <-time.After(20 * time.Second):
		t.Error("the server did not exit when its stdin closed")
	}
}

// `golm acp` over a real pipe.
func TestACPSubprocessSpeaksACP(t *testing.T) {
	p := startRPC(t, "acp", "--config", catConfig(t))

	init := p.call(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": acp.MethodInitialize,
		"params": map[string]any{"protocolVersion": acp.Version,
			"clientCapabilities": map[string]any{}},
	})
	result, ok := init["result"].(map[string]any)
	if !ok {
		t.Fatalf("initialize returned %v (stderr: %s)", init, p.errText())
	}
	if _, ok := result["protocolVersion"]; !ok {
		t.Errorf("no protocolVersion in the reply: %v", result)
	}

	sess := p.call(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": acp.MethodNewSession,
		"params": map[string]any{"cwd": t.TempDir(), "mcpServers": []any{}},
	})
	sres, ok := sess["result"].(map[string]any)
	if !ok {
		t.Fatalf("session/new returned %v (stderr: %s)", sess, p.errText())
	}
	if sres["sessionId"] == "" || sres["sessionId"] == nil {
		t.Errorf("session/new produced no session id: %v", sres)
	}

	_ = p.in.Close()
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("exit after stdin closed: %v (stderr: %s)", err, p.errText())
		}
	case <-time.After(20 * time.Second):
		t.Error("acp did not exit when its stdin closed")
	}
}

// The exit codes a shell script would branch on.
func TestBinaryExitCodes(t *testing.T) {
	bin := golmBinary(t)
	for _, c := range []struct {
		name string
		args []string
		want int
	}{
		{"version", []string{"--version"}, 0},
		{"help", []string{"--help"}, 0},
		{"unknown flag", []string{"--definitely-not-a-flag"}, 2},
		{"missing config", []string{"--config", "/nonexistent/golm.json", "hi"}, 1},
		{"a2a-serve without config", []string{"a2a-serve"}, 1},
	} {
		cmd := exec.Command(bin, c.args...)
		cmd.Stdin = strings.NewReader("")
		_ = cmd.Run()
		got := cmd.ProcessState.ExitCode()
		if got != c.want {
			t.Errorf("%s: exit %d, want %d", c.name, got, c.want)
		}
	}
}

// --version must print to STDOUT and nothing else.
func TestVersionGoesToStdoutAlone(t *testing.T) {
	cmd := exec.Command(golmBinary(t), "--version")
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.HasPrefix(out.String(), "golm ") {
		t.Errorf("stdout = %q", out.String())
	}
	if strings.TrimSpace(errb.String()) != "" {
		t.Errorf("--version wrote to stderr: %q", errb.String())
	}
}
