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
	"syscall"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/build"
	"github.com/leelsey/golm/openaiapi"
)

const defaultServeAddr = "127.0.0.1:8000"

func runServe(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("golm serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	bf := addBackendFlags(fs)
	addr := fs.String("addr", defaultServeAddr, "listen address (bind an interface explicitly to expose it)")
	tokenEnv := fs.String("token-env", "", "env var holding a bearer token required on every request")
	orchestrate := fs.Bool("orchestrate", false, "also serve the whole team under one model name")
	teamName := fs.String("team-name", "team", "model name for the orchestrated team (with --orchestrate)")
	fs.Usage = func() { usage(stderr) }
	if code := parseScoped(fs, args, stderr, "serve"); code >= 0 {
		return code
	}
	cfgPath := bf.resolveConfigPath(stderr)
	if cfgPath == "" {
		fmt.Fprintln(stderr, "golm serve: requires a config (pass --config or run where golm.json is discoverable)")
		return 2
	}
	cfg, err := golm.LoadConfig(cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "golm serve:", err)
		return 1
	}

	var token string
	if *tokenEnv != "" {
		if token = os.Getenv(*tokenEnv); token == "" {
			fmt.Fprintf(stderr, "golm serve: --token-env %s is empty or unset\n", *tokenEnv)
			return 2
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv, closeAll, err := bf.openaiServer(ctx, cfg, token, *orchestrate, *teamName, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "golm serve:", err)
		return 1
	}
	defer closeAll()
	if len(srv.Models()) == 0 {
		fmt.Fprintln(stderr, "golm serve: the config defines no agents")
		return 1
	}

	fmt.Fprintf(stderr, "golm serve: OpenAI-compatible API on %s\n", *addr)
	for _, m := range srv.Models() {
		fmt.Fprintf(stderr, "golm serve:   model %q\n", m)
	}
	fmt.Fprintf(stderr, "golm serve: try: curl %s/v1/models\n", displayURL(*addr))
	warnOpenListener(stderr, "serve", *addr, token,
		"anyone who can reach it can spend your API credits and reach every tool these agents have")

	if err := runHTTPServer(ctx, *addr, srv.Handler()); err != nil {
		fmt.Fprintln(stderr, "golm serve:", err)
		return 1
	}
	return 0
}

func (bf *backendFlags) openaiServer(ctx context.Context, cfg *golm.Config, token string, orchestrate bool, teamName string, stderr io.Writer) (*openaiapi.Server, func(), error) {
	opts, gate, err := bf.buildOptions(cfg, stderr)
	if err != nil {
		return nil, nil, err
	}
	closers := []func() error{gate.close}
	closeAll := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			_ = closers[i]()
		}
	}

	if bf.ask {
		fmt.Fprintln(stderr, "golm serve: warning: --ask has no terminal to ask at here; gated tool calls will be refused")
	}

	store, err := openStore(bf.store, true)
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	srv := openaiapi.NewServer()
	srv.AuthToken = token
	srv.Store = store
	srv.Logger = opts.Logger

	rt, agents, err := build.NewMany(ctx, opts)
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	closers = append(closers, rt.Close)
	for _, p := range cfg.Agents {
		srv.Add(p.Name, agents[p.Name], p.Description)
	}
	if orchestrate {
		ort, orc, err := bf.orchestrationWith(ctx, opts, cfg, stderr)
		if err != nil {
			closeAll()
			return nil, nil, err
		}
		closers = append(closers, ort.Close)
		srv.Add(teamName, orc, "the whole team, routing and delegating internally")
	}
	return srv, closeAll, nil
}

func displayURL(addr string) string {
	if len(addr) > 0 && addr[0] == ':' {
		return "http://127.0.0.1" + addr
	}
	return "http://" + addr
}
