// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

//go:build unix

package proc

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The reason the package exists.
func TestKillGroupReachesWhatTheChildStarted(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "grandchild.pid")

	c := exec.Command("sh", "-c", "(echo $$ > "+pidFile+"; exec sleep 30) & exit 0")
	Bound(c, 200*time.Millisecond)
	if err := c.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	pid := waitForPid(t, pidFile)
	if !alive(pid) {
		t.Fatalf("grandchild %d was never alive; the test proves nothing", pid)
	}

	if err := KillGroup(c); err != nil {
		t.Fatalf("KillGroup: %v", err)
	}
	_ = c.Wait()

	deadline := time.Now().Add(3 * time.Second)
	for alive(pid) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatal("the grandchild outlived KillGroup: the group was not signalled")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Bound's other half, which matters even where process groups do not exist.
func TestBoundReleasesTheWaitEvenWithAGrandchildHoldingThePipes(t *testing.T) {
	c := exec.Command("sh", "-c", "sleep 30 & exit 0")
	out, err := c.StdoutPipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	Bound(c, 150*time.Millisecond)
	if err := c.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = KillGroup(c) }()
	_ = out

	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return: WaitDelay is not bounding the pipe wait")
	}
}

func TestBoundSetsTheProcessGroupAndDelay(t *testing.T) {
	c := exec.Command("true")
	Bound(c, 42*time.Millisecond)
	if c.WaitDelay != 42*time.Millisecond {
		t.Errorf("WaitDelay = %v, want 42ms", c.WaitDelay)
	}
	if c.SysProcAttr == nil || !c.SysProcAttr.Setpgid {
		t.Error("Setpgid was not applied; a kill would reach the child only")
	}
}

// An already-finished process must yield os.ErrProcessDone and not nil.
func TestKillGroupOnAnExitedProcessReportsProcessDone(t *testing.T) {
	c := exec.Command("true")
	Bound(c, time.Second)
	if err := c.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	_ = c.Wait()
	err := KillGroup(c)
	if !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("KillGroup after exit = %v, want os.ErrProcessDone", err)
	}
}

func TestKillGroupIsNilSafe(t *testing.T) {
	if err := KillGroup(nil); err != nil {
		t.Errorf("KillGroup(nil) = %v, want nil", err)
	}
	if err := KillGroup(exec.Command("true")); err != nil {
		t.Errorf("KillGroup(unstarted) = %v, want nil", err)
	}
}

func waitForPid(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if b, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the grandchild never recorded its pid")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }
