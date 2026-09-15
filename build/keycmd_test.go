// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package build

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestKeyFromCmd(t *testing.T) {
	got, err := keyFromCmd(context.Background(), "echo sk-test")
	if err != nil {
		t.Fatalf("keyFromCmd: %v", err)
	}
	if got != "sk-test" {
		t.Errorf("key = %q, want %q", got, "sk-test")
	}
}

func TestKeyFromCmdEmpty(t *testing.T) {
	if _, err := keyFromCmd(context.Background(), "   "); err == nil {
		t.Error("an empty command should be an error, not an empty key")
	}
}

func TestKeyFromCmdHonoursDeadline(t *testing.T) {
	requireShell(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	// The sleep is deliberately not the shell's last command, so it runs as a
	// grandchild holding the output pipes: killing the shell alone is not enough.
	if _, err := keyFromCmd(ctx, `sh -c "sleep 20; :"`); err == nil {
		t.Fatal("a command past the deadline should fail, not return an empty key")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want it to wrap context.DeadlineExceeded", err)
	}

	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("waited %v, want the deadline to cut it short", elapsed)
	}
}

func TestKeyFromCmdBoundsItselfWithoutADeadline(t *testing.T) {
	if DefaultKeyCmdTimeout <= 0 {
		t.Fatal("DefaultKeyCmdTimeout must be positive, or a hung helper hangs the process")
	}
}

func TestKeyFromCmdReportsStderrNotStdout(t *testing.T) {
	requireShell(t)

	_, err := keyFromCmd(context.Background(), `sh -c "echo LEAKED-KEY; echo keychain is locked >&2; exit 3"`)
	if err == nil {
		t.Fatal("expected the command's failure")
	}
	if !strings.Contains(err.Error(), "keychain is locked") {
		t.Errorf("err = %v, want the command's stderr in it", err)
	}
	if strings.Contains(err.Error(), "LEAKED-KEY") {
		t.Errorf("err = %v, must not quote stdout — that is where the key is", err)
	}
}

func TestTruncMarksWhatItCuts(t *testing.T) {
	if got := trunc("abc", 10); got != "abc" {
		t.Errorf("trunc under the cap = %q, want it unchanged", got)
	}
	got := trunc(strings.Repeat("x", 20), 5)
	if !strings.HasPrefix(got, "xxxxx") || !strings.Contains(got, "truncated") {
		t.Errorf("trunc = %q, want the cut marked", got)
	}
}

func requireShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
}
