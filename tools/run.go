// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/internal/proc"
)

const (
	maxRunOutput   = 64 << 10
	defaultRunTime = 60 * time.Second
	defaultMaxArgs = 256

	killGrace = 2 * time.Second
)

// Runner executes programs for the model, from a list the operator wrote.
type Runner struct {
	Allow []string

	Dir string

	Timeout time.Duration

	Env []string

	Inherit []string

	DenyArgs []string

	MaxArgs int
}

type runArgs struct {
	Command string   `json:"command" jsonschema:"the program to run; must be one the operator allowed"`
	Args    []string `json:"args,omitempty" jsonschema:"arguments, one per element; no shell is involved"`
}

func (r *Runner) env() []string {
	if r.Env != nil {
		return r.Env
	}
	return proc.MinimalEnv(r.Inherit)
}

// Tool returns the run tool, or nil when nothing is allowed.
func (r *Runner) Tool() golm.Tool {
	if r == nil || len(r.Allow) == 0 {
		return nil
	}
	desc := "Run one of these programs: " + strings.Join(r.Allow, ", ") +
		". Arguments are passed directly, without a shell."
	t := golm.NewTypedTool("run", desc,
		func(ctx context.Context, in runArgs) (string, error) {
			cmd := strings.TrimSpace(in.Command)
			if cmd == "" {
				return "", fmt.Errorf("no command given")
			}
			if !slices.Contains(r.Allow, cmd) {
				return "", fmt.Errorf("%q is not allowed here; allowed: %s", cmd, strings.Join(r.Allow, ", "))
			}
			if err := r.checkArgs(in.Args); err != nil {
				return "", err
			}
			timeout := r.Timeout
			if timeout <= 0 {
				timeout = defaultRunTime
			}
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			c := exec.CommandContext(ctx, cmd, in.Args...)
			c.Dir = r.Dir
			c.Env = r.env()

			c.Stdin = nil

			proc.Bound(c, killGrace)
			c.Cancel = func() error { return proc.KillGroup(c) }
			var out, errOut bytes.Buffer
			c.Stdout = &boundedWriter{w: &out, left: maxRunOutput}
			c.Stderr = &boundedWriter{w: &errOut, left: maxRunOutput}

			runErr := c.Run()
			var sb strings.Builder
			if out.Len() > 0 {
				sb.Write(out.Bytes())
			}
			if errOut.Len() > 0 {
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString("[stderr]\n")
				sb.Write(errOut.Bytes())
			}
			if ctx.Err() != nil {
				return sb.String(), fmt.Errorf("%s timed out after %s", cmd, timeout)
			}
			if runErr != nil {
				return "", fmt.Errorf("%s exited with an error: %v\n%s", cmd, runErr, sb.String())
			}
			if sb.Len() == 0 {
				return "(no output)", nil
			}
			return sb.String(), nil
		})
	return golm.WithTraits(t, golm.ToolTraits{Process: true, Filesystem: true, Network: true})
}

func (r *Runner) checkArgs(args []string) error {
	maxArgs := r.MaxArgs
	if maxArgs <= 0 {
		maxArgs = defaultMaxArgs
	}
	if len(args) > maxArgs {
		return fmt.Errorf("%d arguments is over the limit of %d", len(args), maxArgs)
	}
	for _, a := range args {
		if strings.ContainsRune(a, 0) {
			return fmt.Errorf("arguments must not contain NUL bytes")
		}
		for _, bad := range r.DenyArgs {
			if bad != "" && strings.Contains(strings.ToLower(a), strings.ToLower(bad)) {
				return fmt.Errorf("argument %q contains %q, which is not allowed here", trunc(a, 80), bad)
			}
		}
	}
	return nil
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

type boundedWriter struct {
	w    *bytes.Buffer
	left int
	cut  bool
}

func (b *boundedWriter) Write(p []byte) (int, error) {
	n := len(p)
	if b.left <= 0 {
		if !b.cut {
			b.w.WriteString("\n[output truncated]")
			b.cut = true
		}
		return n, nil
	}
	if len(p) > b.left {
		p = p[:b.left]
	}
	b.w.Write(p)
	b.left -= len(p)
	return n, nil
}
