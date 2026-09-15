// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/a2a"
	"github.com/leelsey/golm/build"
	"github.com/leelsey/golm/internal/rpc"
	"github.com/leelsey/golm/mcp"
	"github.com/leelsey/golm/netbus"
)

func runMCPServer(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("golm mcp-server", flag.ContinueOnError)
	fs.SetOutput(stderr)
	bf := addBackendFlags(fs)
	name := fs.String("name", "golm", "MCP server name")
	if code := parseScoped(fs, args, stderr, "mcp"); code >= 0 {
		return code
	}
	srv := mcp.NewServer(*name, golm.Version)
	ctx := context.Background()
	if cfgPath := bf.resolveConfigPath(stderr); cfgPath != "" {
		cfg, err := golm.LoadConfig(cfgPath)
		if err != nil {
			fmt.Fprintln(stderr, "golm mcp-server:", err)
			return 1
		}

		opts, gate, err := bf.buildOptions(cfg, stderr)
		if err != nil {
			fmt.Fprintln(stderr, "golm mcp-server:", err)
			return 1
		}
		defer gate.close()
		if bf.ask {
			fmt.Fprintln(stderr, "golm mcp-server: warning: --ask has no terminal to ask at here; gated tool calls will be refused")
		}

		rt, agents, err := build.NewMany(ctx, opts)
		if err != nil {
			fmt.Fprintln(stderr, "golm mcp-server:", err)
			return 1
		}
		defer rt.Close()
		for _, p := range cfg.Agents {
			desc := p.Description
			if desc == "" {
				desc = "Run the " + p.Name + " agent."
			}
			srv.AddAgent(p.Name, desc, agents[p.Name])
		}
		fmt.Fprintf(stderr, "golm mcp-server: exposing %d agent(s) over stdio\n", len(cfg.Agents))
	} else {
		fmt.Fprintln(stderr, "golm mcp-server: no --config; serving with no tools")
	}
	if err := srv.Serve(context.Background(), rpc.Stdio()); err != nil {
		fmt.Fprintln(stderr, "golm mcp-server:", err)
		return 1
	}
	return 0
}

const (
	defaultA2AAddr = "127.0.0.1:8080"
	defaultBusAddr = "127.0.0.1:7777"
)

func warnOpenListener(stderr io.Writer, cmd, addr, token, risk string) {
	if addrIsLoopback(addr) {
		return
	}
	if token == "" {
		fmt.Fprintf(stderr, "golm %s: WARNING serving without authentication on %s — %s. "+
			"Use --token-env, or bind 127.0.0.1.\n", cmd, addr, risk)
		return
	}
	fmt.Fprintf(stderr, "golm %s: WARNING %s is not loopback and nothing here serves TLS — "+
		"the bearer token crosses the network in clear. Put it behind a TLS proxy.\n", cmd, addr)
}

func addrIsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func a2aServerFor(card a2a.AgentCard, ag *golm.Agent, authToken string) *a2a.Server {
	srv := a2a.NewServer(card, a2a.AgentHandler(ag))
	srv.AuthToken = authToken
	return srv
}

const a2aDrainTimeout = 10 * time.Second

func runA2AServe(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("golm a2a-serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	bf := addBackendFlags(fs)
	addr := fs.String("addr", defaultA2AAddr, "listen address (bind an interface explicitly to expose it)")
	urlFlag := fs.String("url", "", "advertised agent-card URL (default derived from -addr)")
	tokenEnv := fs.String("token-env", "", "env var holding a bearer token required on every RPC")
	if code := parseScoped(fs, args, stderr, "a2a"); code >= 0 {
		return code
	}
	cfgPath := bf.resolveConfigPath(stderr)
	if cfgPath == "" {
		fmt.Fprintln(stderr, "golm a2a-serve: --config is required")
		return 1
	}

	var authToken string
	if *tokenEnv != "" {
		authToken = os.Getenv(*tokenEnv)
		if authToken == "" {
			fmt.Fprintf(stderr, "golm a2a-serve: --token-env %s is empty or unset\n", *tokenEnv)
			return 2
		}
	}
	cfg, err := golm.LoadConfig(cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "golm a2a-serve:", err)
		return 1
	}
	name := bf.agent
	if name == "" {
		name = cfg.DefaultAgent
	}
	if name == "" && len(cfg.Agents) > 0 {
		name = cfg.Agents[0].Name
	}
	if name == "" {
		fmt.Fprintln(stderr, "golm a2a-serve: no agent to expose")
		return 1
	}

	opts, gate, err := bf.buildOptions(cfg, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "golm a2a-serve:", err)
		return 1
	}
	defer gate.close()
	if bf.ask {
		fmt.Fprintln(stderr, "golm a2a-serve: warning: --ask has no terminal to ask at here; gated tool calls will be refused")
	}
	rt, err := bf.runtimeFor(context.Background(), opts, name)
	if err != nil {
		fmt.Fprintln(stderr, "golm a2a-serve:", err)
		return 1
	}
	defer rt.Close()
	ag := rt.Agent
	persona, _ := cfg.Agent(name)
	desc := persona.Description
	if desc == "" {
		desc = "golm agent " + name
	}
	url := *urlFlag
	if url == "" {
		url = "http://localhost" + *addr
		if !strings.HasPrefix(*addr, ":") {
			url = "http://" + *addr
		}
	}
	card := a2a.CardForAgent(name, desc, url+"/")
	srv := a2aServerFor(card, ag, authToken)
	fmt.Fprintf(stderr, "golm a2a-serve: exposing agent %q at %s (card: %s%s)\n", name, *addr, url, "/.well-known/agent-card.json")
	warnOpenListener(stderr, "a2a-serve", *addr, authToken,
		"anyone who can reach it can give the agent work and read any task id they can guess")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	drain := func() {
		sctx, cancel := context.WithTimeout(context.Background(), a2aDrainTimeout)
		defer cancel()
		srv.TaskStore().Shutdown(sctx)
	}
	if err := runHTTPServer(ctx, *addr, srv.HTTPHandler(), drain); err != nil {
		fmt.Fprintln(stderr, "golm a2a-serve:", err)
		return 1
	}
	return 0
}

const (
	drainGrace = 2 * time.Second

	shutdownBudget = 10 * time.Second
)

func runHTTPServer(ctx context.Context, addr string, h http.Handler, onShutdown ...func()) error {
	base, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()

	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return base },
	}
	for _, f := range onShutdown {
		srv.RegisterOnShutdown(f)
	}
	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownBudget)
		defer cancel()

		shDone := make(chan error, 1)
		go func() { shDone <- srv.Shutdown(shutdownCtx) }()
		var shErr error
		select {
		case shErr = <-shDone:
		case <-time.After(drainGrace):
			cancelBase()
			shErr = <-shDone
		}

		if lErr := <-errCh; lErr != nil {
			return lErr
		}
		return shErr
	}
}

func runBus(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("golm bus", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", defaultBusAddr, "listen address (bind an interface explicitly to expose it)")
	tokenEnv := fs.String("token-env", "", "env var holding a bearer token required on every request")
	if code := parseScoped(fs, args, stderr, "bus"); code >= 0 {
		return code
	}
	hub := netbus.NewHub()
	if *tokenEnv != "" {
		hub.AuthToken = os.Getenv(*tokenEnv)
		if hub.AuthToken == "" {
			fmt.Fprintf(stderr, "golm bus: --token-env %s is empty or unset\n", *tokenEnv)
			return 2
		}
	}
	fmt.Fprintf(stderr, "golm bus: hub on %s (POST /publish, GET /subscribe)\n", *addr)

	warnOpenListener(stderr, "bus", *addr, hub.AuthToken,
		"any subscriber reads every event on it — prompts, tool calls and results")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runHTTPServer(ctx, *addr, hub.Handler(), hub.CloseAll); err != nil {
		fmt.Fprintln(stderr, "golm bus:", err)
		return 1
	}
	return 0
}
