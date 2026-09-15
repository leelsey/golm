// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/acp"
	"github.com/leelsey/golm/build"
	"github.com/leelsey/golm/internal/rpc"
)

func runACP(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("golm acp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	bf := addBackendFlags(fs)
	orchestrate := fs.Bool("orchestrate", false, "serve the whole team from --config, not one agent")
	fs.Usage = func() { usage(stderr) }
	if code := parseScoped(fs, args, stderr, "acp"); code >= 0 {
		return code
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	agent, closeAll, err := bf.acpAgent(ctx, bf.resolveConfigPath(stderr), *orchestrate, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "golm acp:", err)
		return 1
	}
	defer closeAll()

	fmt.Fprintf(stderr, "golm acp: serving over stdio (protocol version %d)\n", acp.Version)
	if err := agent.Serve(ctx, rpc.Stdio()); err != nil {
		fmt.Fprintln(stderr, "golm acp:", err)
		return 1
	}
	return 0
}

func (bf *backendFlags) acpAgent(ctx context.Context, cfgPath string, orchestrate bool, stderr io.Writer) (*acp.Agent, func(), error) {
	a := acp.NewAgent(nil)
	a.Info = acp.Implementation{Name: "golm", Title: "golm", Version: golm.Version}
	logger, err := bf.logger(stderr)
	if err != nil {
		return nil, nil, err
	}
	a.Logger = logger

	rt, orc, err := bf.acpRuntime(ctx, cfgPath, orchestrate, a.Approver(), stderr)
	if err != nil {
		return nil, nil, err
	}
	closeAll := func() { _ = rt.Close() }

	if orc != nil {
		a.Runner, a.Orchestrator = orc, orc
	} else {
		a.Runner = rt.Agent
	}

	store, serr := openStore(bf.store, true)
	if serr != nil {
		closeAll()
		return nil, nil, serr
	}
	a.Store = store

	allow, deny := bf.runAllowList(cfgPath), bf.runDenyArgs(cfgPath)
	a.OnInitialized = func() {
		tools := a.FileTools()
		if len(tools) == 0 {
			fmt.Fprintln(stderr, "golm acp: the editor offers no filesystem access; file tools not served")
		} else {
			fmt.Fprintf(stderr, "golm acp: %d file tool(s) served through the editor\n", len(tools))
		}

		switch t := a.Terminals(acp.TerminalRunner{Allow: allow, DenyArgs: deny}); {
		case t != nil:
			tools = append(tools, t)
			fmt.Fprintf(stderr, "golm acp: run served through the editor's terminal (%s)\n",
				strings.Join(allow, ", "))
		case len(allow) > 0:
			fmt.Fprintln(stderr, "golm acp: the editor offers no terminal; the local run tool is used instead")
		}
		if len(tools) == 0 {
			return
		}

		for _, ag := range acpAgents(rt, orc) {
			ag.Tools.Register(tools...)
		}
	}
	return a, closeAll, nil
}

func (bf *backendFlags) runAllowList(cfgPath string) []string {
	if len(bf.allowRun) > 0 {
		return bf.allowRun
	}
	if r := builtinRunConfig(cfgPath); r != nil {
		return r.Allow
	}
	return nil
}

func (bf *backendFlags) runDenyArgs(cfgPath string) []string {
	if r := builtinRunConfig(cfgPath); r != nil {
		return r.DenyArgs
	}
	return nil
}

func builtinRunConfig(cfgPath string) *golm.RunConfig {
	if cfgPath == "" {
		return nil
	}
	cfg, err := golm.LoadConfig(cfgPath)
	if err != nil || cfg.Builtin == nil {
		return nil
	}
	return cfg.Builtin.Run
}

func (bf *backendFlags) acpRuntime(ctx context.Context, cfgPath string, orchestrate bool, approve golm.Approver, stderr io.Writer) (*build.Runtime, *golm.Orchestrator, error) {
	cfg, name, err := bf.resolveConfig(cfgPath)
	if err != nil {
		return nil, nil, err
	}
	opts, gate, err := bf.buildOptions(cfg, stderr)
	if err != nil {
		return nil, nil, err
	}

	opts.Approve = approve
	if opts.Policy == nil && cfg.Policy == nil {
		opts.Policy = askPolicy()
	}

	if orchestrate {
		if cfgPath == "" {
			gate.close()
			return nil, nil, fmt.Errorf("--orchestrate requires a config")
		}
		rt, orc, err := bf.orchestrationWith(ctx, opts, cfg, stderr)
		if err != nil {
			gate.close()
			return nil, nil, err
		}
		rt.OnClose(gate.close)
		return rt, orc, nil
	}
	opts.Agent = name
	rt, err := build.New(ctx, opts)
	if err != nil {
		gate.close()
		return nil, nil, err
	}
	rt.OnClose(gate.close)
	return rt, nil, nil
}

func acpAgents(rt *build.Runtime, orc *golm.Orchestrator) []*golm.Agent {
	if orc != nil {
		var out []*golm.Agent
		for _, info := range orc.Agents() {
			if ag, ok := orc.Get(info.Name); ok {
				out = append(out, ag)
			}
		}
		return out
	}
	if rt.Agent != nil {
		return []*golm.Agent{rt.Agent}
	}
	return nil
}
