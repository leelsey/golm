// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/build"
	"github.com/leelsey/golm/internal/tui"
)

func runTUI(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("golm tui", flag.ContinueOnError)
	fs.SetOutput(stderr)
	bf := addBackendFlags(fs)
	showUsage := fs.Bool("usage", false, "print token usage to stderr after each turn")
	orchestrate := fs.Bool("orchestrate", false, "drive the whole team from --config, not one agent")
	fs.Usage = func() { usage(stderr) }
	if code := parseWithEnv(fs, args, stderr); code >= 0 {
		return code
	}
	cfgPath := bf.resolveConfigPath(stderr)
	if !validModalities(bf.modalities, stderr) {
		return 2
	}
	ctx, interrupts, stop := bf.sessionContext()
	defer stop()

	var (
		rt  *build.Runtime
		orc *golm.Orchestrator
		err error
	)
	if *orchestrate {
		rt, orc, err = bf.orchestration(ctx, cfgPath, stderr)
	} else {
		rt, err = bf.runtime(ctx, cfgPath, stderr)
	}
	if err != nil {
		fmt.Fprintln(stderr, "golm:", err)
		return 1
	}
	defer rt.Close()
	agent := rt.Agent
	store, err := openStore(bf.store, bf.session != "")
	if err != nil {
		fmt.Fprintln(stderr, "golm:", err)
		return 1
	}
	sess, resumed, err := resumeSession(ctx, store, bf.session)
	if err != nil {
		fmt.Fprintln(stderr, "golm:", err)
		return 1
	}
	if resumed {
		extra := ""
		if orc != nil {
			n, lerr := orc.LoadConversation(ctx, store, sess)
			if lerr != nil {
				fmt.Fprintln(stderr, "golm:", lerr)
			}
			extra = fmt.Sprintf(", %d sub-agent session(s)", n)
		}
		fmt.Fprintf(stderr, "golm: resumed session %s (%d messages%s)\n", sess.ID(), sess.Len(), extra)
	}
	attachArchive(agent, store)

	onFinal := func(res golm.Result) {
		saveMedia(res.Message, stderr)
		if *showUsage {
			printRunUsage(stderr, res)
		}
		reportResult(stderr, res)
	}
	opts := tui.Options{
		Agent: agent, Orchestrator: orc, In: stdin, Out: stdout, OnFinal: onFinal,
		Store: store, Session: sess, Interrupt: interrupts,
	}
	switch err := tui.Run(ctx, opts); {
	case err == nil || errors.Is(err, context.Canceled):
	case errors.Is(err, context.DeadlineExceeded):
		fmt.Fprintln(stderr, "golm: session ended (--timeout reached)")
	default:
		fmt.Fprintln(stderr, "golm:", err)
		return 1
	}
	return 0
}
