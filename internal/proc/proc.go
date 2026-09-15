// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

// Package proc is the small amount of process handling that both the tool runner and the MCP client need.
package proc

import (
	"os/exec"
	"time"
)

// Bound puts c in its own process group and bounds how long Wait holds on to its pipes after it exits.
func Bound(c *exec.Cmd, waitDelay time.Duration) {
	setGroup(c)
	c.WaitDelay = waitDelay
}

// KillGroup kills c's whole process group, falling back to the process itself when the group is already gone.
func KillGroup(c *exec.Cmd) error {
	if c == nil || c.Process == nil {
		return nil
	}
	return killGroup(c)
}
