// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/leelsey/golm"
)

func askPolicy() *golm.PolicyConfig {
	return &golm.PolicyConfig{
		AskTraits:  []string{"process", "network", "filesystem"},
		Allow:      readOnlyBuiltins,
		Unreviewed: string(golm.DecisionAsk),
	}
}

var readOnlyBuiltins = []string{"read_file", "list_dir", "search", "read_skill"}

type approver struct {
	gate chan struct{}

	reading sync.Mutex
	in      *bufio.Reader
	out     io.Writer

	mu sync.Mutex

	always map[string]bool
}

func newApprover(out io.Writer) *approver {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil
	}
	w := out
	if f, ok := out.(*os.File); !ok || !isTerminal(f) {
		w = tty
	}
	return &approver{
		gate: make(chan struct{}, 1), in: bufio.NewReader(tty), out: w,
		always: map[string]bool{},
	}
}

// Approve asks, and returns the answer.
func (a *approver) Approve(ctx context.Context, req golm.ToolRequest) (bool, error) {
	if a.remembered(req.Call.Name) {
		return true, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	select {
	case a.gate <- struct{}{}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	defer func() { <-a.gate }()

	if a.remembered(req.Call.Name) {
		return true, nil
	}

	fmt.Fprintf(a.out, "\n%s\n", describeCall(req))
	fmt.Fprint(a.out, "allow? [y]es / [n]o / [a]lways for this tool: ")

	type answer struct {
		line string
		err  error
	}

	ch := make(chan answer, 1)
	go func() {
		a.reading.Lock()
		line, err := a.in.ReadString('\n')
		a.reading.Unlock()
		ch <- answer{line, err}
	}()
	select {
	case <-ctx.Done():
		fmt.Fprintln(a.out, "(run ended; taking that as no)")
		return false, ctx.Err()
	case got := <-ch:
		if got.err != nil && strings.TrimSpace(got.line) == "" {
			return false, nil
		}
		switch strings.ToLower(strings.TrimSpace(got.line)) {
		case "y", "yes":
			return true, nil
		case "a", "always":
			a.remember(req.Call.Name)
			return true, nil
		default:
			return false, nil
		}
	}
}

func (a *approver) remembered(tool string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.always[tool]
}

func (a *approver) remember(tool string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.always[tool] = true
}

const maxShownInput = 400

func describeCall(req golm.ToolRequest) string {
	var b strings.Builder
	b.WriteString("tool approval: " + req.Call.Name)
	if req.Agent != "" {
		b.WriteString(" (agent " + req.Agent + ")")
	}
	if tr, ok := golm.TraitsOf(req.Tool); ok {
		if names := traitList(tr); names != "" {
			b.WriteString(" [" + names + "]")
		}
	} else {
		b.WriteString(" [undeclared capabilities]")
	}
	if in := prettyInput(req.Call.Input); in != "" {
		b.WriteString("\n  " + in)
	}
	return b.String()
}

func traitList(tr golm.ToolTraits) string {
	var out []string
	for name, on := range map[string]bool{
		"read-only": tr.ReadOnly, "filesystem": tr.Filesystem,
		"network": tr.Network, "process": tr.Process,
	} {
		if on {
			out = append(out, name)
		}
	}

	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return strings.Join(out, ", ")
}

func prettyInput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return truncateRunes(string(raw), maxShownInput)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return truncateRunes(string(raw), maxShownInput)
	}
	return truncateRunes(string(b), maxShownInput)
}

func truncateRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
