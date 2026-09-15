// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/build"
	"github.com/leelsey/golm/netbus"
)

func orchestrate(ctx context.Context, o *golm.Orchestrator, conv *golm.Session, prompt string, noStream bool, stdout, stderr io.Writer) (golm.Result, error) {
	if noStream {
		res, err := o.Run(ctx, conv, prompt)
		if err == nil {
			fmt.Fprintln(stdout, res.Text())
		}
		return res, err
	}

	res, err := o.Stream(ctx, conv, prompt, func(ev golm.StreamEvent) error {
		switch ev.Type {
		case golm.EventTextDelta:

			if ev.Depth > 0 {
				fmt.Fprint(stderr, ev.Text)
				return nil
			}
			fmt.Fprint(stdout, ev.Text)
		case golm.EventThinkingDelta:
			fmt.Fprint(stderr, ev.Text)
		case golm.EventAgentStart:
			fmt.Fprintf(stderr, "\n[%s] ↳ %s\n", ev.Agent, trunc(ev.Text, 100))
		case golm.EventAgentStop:
			fmt.Fprintf(stderr, "\n[%s] ↳ done\n", ev.Agent)
		}
		return nil
	})
	fmt.Fprintln(stdout)
	return res, err
}

func eventDetail(ev golm.Event) string {
	switch d := ev.Data.(type) {
	case golm.StepEvent:
		return fmt.Sprintf(" %d: %s (%s)", d.Step, d.StopReason, d.Usage)
	case golm.ToolCallEvent:
		return " " + d.Name + " " + trunc(string(d.Input), 120)
	case golm.ToolResultEvent:
		if d.IsError {
			return " " + d.Name + " error: " + trunc(d.Content, 200)
		}
		return " " + d.Name + " ok"
	case golm.RouteEvent:

		return " → " + d.Agent + " (" + d.Reason + ")"
	case string:
		if ev.Kind == "error" {
			return ": " + trunc(d, 200)
		}
	}
	return ""
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

func runOrchestrator(ctx context.Context, bf *backendFlags, configPath, prompt string, showUsage, noStream bool, stdout, stderr io.Writer) int {
	if configPath == "" {
		fmt.Fprintln(stderr, "golm: --orchestrate requires --config")
		return 1
	}
	cfg, err := golm.LoadConfig(configPath)
	if err != nil {
		fmt.Fprintln(stderr, "golm:", err)
		return 1
	}

	bus := golm.NewBus()
	var bridgeDone chan struct{}
	if cfg.Bus != "" {
		nc := netbus.NewClient(cfg.Bus)
		if cfg.BusTokenEnv != "" {
			token := os.Getenv(cfg.BusTokenEnv)
			if token == "" {
				fmt.Fprintf(stderr, "golm: bus_token_env %s is empty or unset\n", cfg.BusTokenEnv)
				return 1
			}
			nc = nc.WithToken(token)
		}
		bridgeDone = make(chan struct{})
		go func() { defer close(bridgeDone); _ = nc.Bridge(ctx, bus) }()
		fmt.Fprintf(stderr, "golm: bridging events to network bus %s\n", cfg.Bus)
	}
	events, unsub := bus.Subscribe("*")
	defer unsub()
	done := make(chan struct{})
	go func() {
		for ev := range events {
			fmt.Fprintf(stderr, "[%s] %s%s\n", ev.Agent, ev.Kind, eventDetail(ev))
		}
		close(done)
	}()

	opts, gate, err := bf.buildOptions(cfg, stderr)
	if err != nil {
		bus.Close()
		<-done
		fmt.Fprintln(stderr, "golm:", err)
		return 1
	}
	defer gate.close()

	rt, o, err := build.NewOrchestrator(ctx, opts, bus)
	if err != nil {
		bus.Close()
		<-done
		fmt.Fprintln(stderr, "golm:", err)
		return 1
	}
	defer rt.Close()
	if n := len(rt.Tools.List()); n > 0 {
		fmt.Fprintf(stderr, "golm: %d tool(s) available (skills, MCP, A2A)\n", n)
	}

	store, err := openStore(bf.store, bf.session != "")
	if err != nil {
		bus.Close()
		<-done
		fmt.Fprintln(stderr, "golm:", err)
		return 1
	}
	conv, resumed, err := resumeSession(ctx, store, bf.session)
	if err != nil {
		bus.Close()
		<-done
		fmt.Fprintln(stderr, "golm:", err)
		return 1
	}
	if resumed {
		n, err := o.LoadConversation(ctx, store, conv)
		if err != nil {
			fmt.Fprintln(stderr, "golm:", err)
		}
		fmt.Fprintf(stderr, "golm: resumed session %s (%d messages, %d sub-agent session(s))\n",
			conv.ID(), conv.Len(), n)
	}

	bf.applyToOrchestration(o, stderr)

	res, err := orchestrate(ctx, o, conv, prompt, noStream, stdout, stderr)

	if store != nil {
		if serr := o.SaveConversation(ctx, store, conv); serr != nil {
			fmt.Fprintln(stderr, "golm: not saved:", serr)
		}
	}
	if showUsage {
		printRunUsage(stderr, res)
		if du := o.Usage(); du != (golm.Usage{}) {
			fmt.Fprintf(stderr, "golm: delegated %s\n", formatUsage(du))
		}
	}
	bus.Close()
	<-done
	if bridgeDone != nil {
		select {
		case <-bridgeDone:
		case <-time.After(3 * time.Second):
			fmt.Fprintln(stderr, "golm: warning: bus bridge did not drain in time; final events may be lost")
		}
	}
	if err != nil {
		reportResult(stderr, res)
		return exitErr(stderr, ctx, err)
	}
	return reportResult(stderr, res)
}
