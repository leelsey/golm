// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

//go:build unix

package proc

import (
	"os/exec"
	"syscall"
)

func setGroup(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Setpgid = true
}

func killGroup(c *exec.Cmd) error {
	if err := syscall.Kill(-c.Process.Pid, syscall.SIGKILL); err != nil {
		return c.Process.Kill()
	}
	return nil
}
