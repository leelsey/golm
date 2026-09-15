// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

//go:build !unix

package proc

import "os/exec"

func setGroup(*exec.Cmd) {}

func killGroup(c *exec.Cmd) error { return c.Process.Kill() }
