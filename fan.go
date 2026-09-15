// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Fan limits. A model asked to split work will occasionally ask for a thousand pieces of it.
const (
	// DefaultMaxFan bounds how many tasks one fan-out may carry.
	DefaultMaxFan = 16
	// DefaultFanParallel bounds how many of them run at once.
	DefaultFanParallel = 4
)

// FanResult is one task's outcome.
type FanResult struct {
	Index int
	Task  string
	Text  string
	Usage Usage

	Err error
}

// Fan runs one sub-agent over several tasks concurrently.
func (o *Orchestrator) Fan(ctx context.Context, agent string, tasks []string) ([]FanResult, error) {
	sub, ok := o.Get(agent)
	if !ok {
		return nil, fmt.Errorf("orchestrator: unknown agent %q", agent)
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("orchestrator: fan-out over %q was given no tasks", agent)
	}
	if max := o.maxFan(); len(tasks) > max {
		return nil, fmt.Errorf("orchestrator: %d tasks is over the fan-out limit of %d; ask for fewer, or raise MaxFan", len(tasks), max)
	}
	depth := delegationDepth(ctx) + 1
	if limit := o.maxDepth(); depth > limit {
		return nil, fmt.Errorf("%w: fanning out to %q is %d levels down, past the limit of %d",
			ErrDelegationTooDeep, agent, depth, limit)
	}
	ctx = context.WithValue(ctx, delegationDepthKey{}, depth)

	out := make([]FanResult, len(tasks))
	sem := make(chan struct{}, o.fanParallel())
	var wg sync.WaitGroup
	for i, task := range tasks {
		out[i] = FanResult{Index: i, Task: task}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():

			out[i].Err = ctx.Err()
			continue
		}
		wg.Add(1)
		go func(i int, task string) {
			defer wg.Done()
			defer func() { <-sem }()

			defer func() {
				if r := recover(); r != nil {
					out[i].Err = fmt.Errorf("orchestrator: fan-out task %d panicked: %v", i, r)
				}
			}()
			res, err := o.runSub(ctx, sub, NewSession(), agent, task, depth)
			o.addUsage(res.Usage)
			out[i].Text, out[i].Usage, out[i].Err = res.Text(), res.Usage, err
		}(i, task)
	}
	wg.Wait()
	return out, nil
}

func (o *Orchestrator) maxFan() int {
	if o.MaxFan > 0 {
		return o.MaxFan
	}
	return DefaultMaxFan
}

func (o *Orchestrator) fanParallel() int {
	if o.FanParallel > 0 {
		return o.FanParallel
	}
	return DefaultFanParallel
}

type fanArgs struct {
	Tasks []string `json:"tasks" jsonschema:"the independent pieces of work, one per element; each is handled on its own with no knowledge of the others"`
}

// AsFanTool exposes Fan to the model.
func (o *Orchestrator) AsFanTool(agent, description string) Tool {
	if description == "" {
		description = fmt.Sprintf(
			"Hand several INDEPENDENT tasks to the %s agent at once. Each is answered separately and cannot see the others; "+
				"use the plain %s tool for work that has to build on a previous answer.", agent, agent)
	}
	return NewTypedTool(agent+"_each", description,
		func(ctx context.Context, in fanArgs) (string, error) {
			results, err := o.Fan(ctx, agent, in.Tasks)
			if err != nil {
				return "", err
			}
			return formatFan(results), nil
		})
}

func formatFan(results []FanResult) string {
	var b strings.Builder
	var failed int
	for _, r := range results {
		fmt.Fprintf(&b, "## %d. %s\n", r.Index+1, firstLine(r.Task))
		if r.Err != nil {
			failed++
			fmt.Fprintf(&b, "FAILED: %v\n\n", r.Err)
			continue
		}
		text := strings.TrimSpace(r.Text)
		if text == "" {
			text = "(no answer)"
		}
		b.WriteString(text + "\n\n")
	}
	if failed > 0 {
		fmt.Fprintf(&b, "(%d of %d task(s) failed)\n", failed, len(results))
	}
	return strings.TrimSpace(b.String())
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return truncRunes(s, 80)
}
