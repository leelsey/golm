// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/tools"
)

func runTool(t *testing.T, r *tools.Runner) golm.Tool {
	t.Helper()
	tool := r.Tool()
	if tool == nil {
		t.Fatal("no run tool; the allowlist is empty")
	}
	return tool
}

func call(t *testing.T, tool golm.Tool, args any) (string, error) {
	t.Helper()
	b, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out, err := tool.Execute(context.Background(), b)
	var sb strings.Builder
	for _, c := range out {
		if txt, ok := c.(golm.Text); ok {
			sb.WriteString(txt.Text)
		}
	}
	return sb.String(), err
}

// Some programs are a shell in disguise once you pick the right flag.
func TestRunnerDeniedArgumentPatterns(t *testing.T) {
	tool := runTool(t, &tools.Runner{
		Allow:    []string{"echo"},
		DenyArgs: []string{"-c", "--exec"},
	})
	for _, bad := range []string{"-c", "--EXEC=rm", "x-c-y"} {
		if _, err := call(t, tool, map[string]any{"command": "echo", "args": []string{bad}}); err == nil {
			t.Errorf("argument %q was allowed", bad)
		}
	}
	if out, err := call(t, tool, map[string]any{"command": "echo", "args": []string{"hello"}}); err != nil {
		t.Errorf("an ordinary argument was refused: %v (%s)", err, out)
	}
}

// A NUL truncates the argument at the exec boundary.
func TestRunnerRejectsNULInArguments(t *testing.T) {
	tool := runTool(t, &tools.Runner{Allow: []string{"echo"}})
	_, err := call(t, tool, map[string]any{"command": "echo", "args": []string{"safe\x00--dangerous"}})
	if err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Errorf("err = %v, want a refusal naming the NUL byte", err)
	}
}

func TestRunnerBoundsArgumentCount(t *testing.T) {
	many := make([]string, 40)
	for i := range many {
		many[i] = "x"
	}
	tool := runTool(t, &tools.Runner{Allow: []string{"echo"}, MaxArgs: 8})
	if _, err := call(t, tool, map[string]any{"command": "echo", "args": many}); err == nil {
		t.Error("an unreasonable argument count was accepted")
	}
}

func TestRunnerStillRefusesWhatIsNotAllowed(t *testing.T) {
	tool := runTool(t, &tools.Runner{Allow: []string{"echo"}})
	if _, err := call(t, tool, map[string]any{"command": "sh", "args": []string{"-c", "id"}}); err == nil {
		t.Error("a command outside the allowlist ran")
	}
	if (&tools.Runner{}).Tool() != nil {
		t.Error("a runner with no allowlist must offer no tool at all")
	}
}

// The defect this covers: CommandContext kills the process it started and nothing that process started.
func TestRunnerTimeoutKillsTheWholeProcessTree(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("needs a POSIX shell")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "forker.sh")

	body := "#!/bin/sh\nsleep 30 &\nsleep 30\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatalf("write: %v", err)
	}
	tool := runTool(t, &tools.Runner{Allow: []string{"/bin/sh"}, Timeout: 300 * time.Millisecond})

	done := make(chan error, 1)
	go func() {
		_, err := call(t, tool, map[string]any{"command": "/bin/sh", "args": []string{script}})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a timed-out command reported success")
		}
		if !strings.Contains(err.Error(), "timed out") {
			t.Errorf("err = %v, want a timeout", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the tool did not return after its timeout: the process tree outlived it")
	}
}
