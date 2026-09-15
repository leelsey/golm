// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

// Example: a multi-agent orchestration embedded in another Go program.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/provider/anthropic"
)

const model = "claude-sonnet-5"

func main() {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		fmt.Println("set ANTHROPIC_API_KEY to run this example")
		return
	}
	prompt := strings.Join(os.Args[1:], " ")
	if prompt == "" {
		prompt = "research the HTTP Strict-Transport-Security header and summarise it"
	}

	p := anthropic.New(key)

	bus := golm.NewBus()
	defer bus.Close()

	o := golm.NewOrchestrator(bus)

	o.Budget = golm.NewBudget(200_000)

	o.StreamDelegates = true

	o.Add("main", "main", &golm.Agent{
		Provider: p, Model: model, MaxSteps: 6,
		System: "You coordinate specialists. Delegate rather than answering yourself when a specialist fits.",
	})
	o.Add("researcher", "sub", &golm.Agent{
		Provider: p, Model: model, MaxSteps: 4,
		System: "You look things up and answer briefly, with the reasoning that supports the answer.",
	})
	o.Add("reviewer", "sub", &golm.Agent{
		Provider: p, Model: model, MaxSteps: 4,
		System: "You review a claim for accuracy and say plainly what is wrong with it.",
	})

	rules, err := golm.RouteRules([]golm.RouteRule{
		{Agent: "researcher", Any: []string{"research", "look up", "what is"}},
		{Agent: "reviewer", Any: []string{"review", "check", "is this right"}},
	})
	if err != nil {
		fail(err)
	}
	o.Router = golm.RouteToLast(rules)

	if err := o.WireDelegation(map[string]string{
		"researcher": "Look something up and report back.",
		"reviewer":   "Review a claim and report what is wrong with it.",
	}); err != nil {
		fail(err)
	}

	o.Main().Tools.Register(o.AsFanTool("researcher", ""))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	conv := golm.NewSession()
	ask(ctx, o, conv, prompt)
	ask(ctx, o, conv, "and what is the most common mistake with it?")

	fmt.Printf("\n--- conversation %s\n", conv.ID())
	fmt.Printf("main:      %s\n", conv.Usage())
	fmt.Printf("delegated: %s\n", o.Usage())
	fmt.Printf("budget:    %s\n", o.Budget)

	for name, s := range o.Sessions(conv.ID()) {
		fmt.Printf("  %-12s %d messages\n", name, s.Len())
	}
	o.Forget(conv.ID())
}

func ask(ctx context.Context, o *golm.Orchestrator, conv *golm.Session, prompt string) {
	fmt.Printf("\n\x1b[1;36myou ▸\x1b[0m %s\n\x1b[1;32mgolm ◂\x1b[0m ", prompt)
	_, err := o.Stream(ctx, conv, prompt, func(ev golm.StreamEvent) error {
		switch ev.Type {
		case golm.EventTextDelta:
			if ev.Depth > 0 {
				fmt.Print("\x1b[2m" + ev.Text + "\x1b[0m")
				return nil
			}
			fmt.Print(ev.Text)
		case golm.EventAgentStart:
			fmt.Printf("\n\x1b[2m  ↳ %s: %s\x1b[0m\n", ev.Agent, ev.Text)
		case golm.EventAgentStop:
			fmt.Printf("\n\x1b[2m  ↳ %s done\x1b[0m\n", ev.Agent)
		}
		return nil
	})
	fmt.Println()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "golm:", err)
	os.Exit(1)
}
