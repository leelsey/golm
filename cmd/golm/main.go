// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT
//
// by Leelsey (le@elsey.me.uk) - https://leelsey.me.uk

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/build"
	"github.com/leelsey/golm/httpdebug"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "mcp-server":
			return runMCPServer(args[1:], stderr)
		case "a2a-serve":
			return runA2AServe(args[1:], stderr)
		case "bus":
			return runBus(args[1:], stderr)
		case "tui":
			return runTUI(args[1:], stdin, stdout, stderr)
		case "config":
			return runConfig(args[1:], stdin, stdout, stderr)
		case "sessions":
			return runSessions(args[1:], stdout, stderr)
		case "serve":
			return runServe(args[1:], stderr)
		case "acp":
			return runACP(args[1:], stderr)
		case "help", "-h", "--help":
			usage(stdout)
			return 0
		}
	}
	fs := flag.NewFlagSet("golm", flag.ContinueOnError)
	fs.SetOutput(stderr)
	bf := addBackendFlags(fs)
	var showVer bool
	fs.BoolVar(&showVer, "version", false, "print version and exit")
	fs.BoolVar(&showVer, "V", false, "print version and exit (shorthand)")
	var (
		noStream    = fs.Bool("no-stream", false, "disable streaming output")
		orchestrate = fs.Bool("orchestrate", false, "run multi-agent orchestration from --config")
		showUsage   = fs.Bool("usage", false, "print token usage to stderr after the run")
	)
	var images, audios stringList
	fs.Var(&images, "image", "image file to attach as input (repeatable)")
	fs.Var(&audios, "audio", "audio file to attach as input (repeatable)")
	fs.Usage = func() { usage(stderr) }
	if code := parseWithEnv(fs, args, stderr); code >= 0 {
		return code
	}
	if showVer {
		fmt.Fprintln(stdout, "golm", golm.Version)
		return 0
	}

	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	hasAttach := len(images) > 0 || len(audios) > 0
	if prompt == "" && !hasAttach {
		if isTerminal(stdin) {
			usage(stdout)
			return 0
		}
		data, _ := io.ReadAll(stdin)
		prompt = strings.TrimSpace(string(data))
	}
	if prompt == "" && !hasAttach {
		fmt.Fprintln(stderr, "golm: no prompt (pass arguments, pipe via stdin, attach --image/--audio, or run 'golm tui')")
		return 2
	}

	ctx, stop := bf.runContext()
	defer stop()

	cfgPath := bf.resolveConfigPath(stderr)
	if !validModalities(bf.modalities, stderr) {
		return 2
	}

	if *orchestrate {
		if len(images) > 0 || len(audios) > 0 || len(bf.modalities) > 0 {
			fmt.Fprintln(stderr, "golm: warning: --image/--audio/--modality are ignored in --orchestrate mode")
		}
		return runOrchestrator(ctx, bf, cfgPath, prompt, *showUsage, *noStream, stdout, stderr)
	}

	rt, err := bf.runtime(ctx, cfgPath, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "golm:", err)
		return 1
	}
	defer rt.Close()
	agent := rt.Agent

	userMsg, err := buildUserMessage(prompt, images, audios)
	if err != nil {
		fmt.Fprintln(stderr, "golm:", err)
		return 1
	}

	warnUnsupportedAttachments(stderr, agent.Provider.Name(), agent.Provider.Capabilities(), images, audios)

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
		fmt.Fprintf(stderr, "golm: resumed session %s (%d messages)\n", sess.ID(), sess.Len())
	}
	attachArchive(agent, store)
	defer saveSession(ctx, store, sess, stderr)

	if *noStream {
		res, err := agent.RunMessage(ctx, sess, userMsg)
		if err != nil {
			if *showUsage {
				printRunUsage(stderr, res)
			}
			reportResult(stderr, res)
			return exitErr(stderr, ctx, err)
		}
		fmt.Fprintln(stdout, res.Text())
		saveMedia(res.Message, stderr)
		if *showUsage {
			printRunUsage(stderr, res)
		}
		return reportResult(stderr, res)
	}

	res, err := agent.StreamMessage(ctx, sess, userMsg, func(ev golm.StreamEvent) error {
		switch ev.Type {
		case golm.EventTextDelta:
			fmt.Fprint(stdout, ev.Text)
		case golm.EventThinkingDelta:
			fmt.Fprint(stderr, ev.Text)
		}
		return nil
	})
	fmt.Fprintln(stdout)
	if *showUsage {
		printRunUsage(stderr, res)
	}
	if err != nil {
		reportResult(stderr, res)
		return exitErr(stderr, ctx, err)
	}
	saveMedia(res.Message, stderr)
	return reportResult(stderr, res)
}

// ExitIncomplete marks an answer that arrived but is not whole.
const ExitIncomplete = 3

func reportResult(stderr io.Writer, res golm.Result) int {
	if res.ToolDenials > 0 {
		fmt.Fprintf(stderr, "golm: %d tool call(s) refused by policy\n", res.ToolDenials)
	}

	if failed := res.ToolErrors - res.ToolDenials; failed > 0 {
		fmt.Fprintf(stderr, "golm: %d tool call(s) failed\n", failed)
	}
	if res.CompactError != nil {
		fmt.Fprintln(stderr, "golm: compaction did not run:", res.CompactError)
	}
	if res.StopReason == golm.StopMaxTokens {
		fmt.Fprintln(stderr, "golm: answer cut off at the output ceiling; raise max_tokens for the whole of it")
		return ExitIncomplete
	}
	return 0
}

func formatUsage(u golm.Usage) string { return "tokens: " + u.String() }

func printRunUsage(w io.Writer, res golm.Result) {
	if len(res.StepUsage) > 1 {
		for i, u := range res.StepUsage {
			fmt.Fprintf(w, "golm: step %d %s\n", i+1, formatUsage(u))
		}
	}
	fmt.Fprintf(w, "golm: %s\n", formatUsage(res.Usage))
}

type backendFlags struct {
	model, provider, cli, baseURL, apiKeyEnv, config, agent, system, think string
	session, store, effort, workspace, audit, memory, toolTags, logLevel   string
	thinkBudget, maxTokens, maxSteps, maxTotalTokens                       int
	timeout                                                                time.Duration
	modalities, skills, allowRun                                           stringList
	debug, readOnly, fetch, fetchInternal, ask                             bool
}

func addBackendFlags(fs *flag.FlagSet) *backendFlags {
	bf := &backendFlags{}
	fs.StringVar(&bf.model, "model", "", "model name")
	fs.StringVar(&bf.model, "m", "", "model name (shorthand)")
	fs.StringVar(&bf.provider, "provider", "", "provider type: anthropic|openai|google (default: inferred from --model)")
	fs.StringVar(&bf.cli, "cli", "", `external agentic CLI backend, e.g. --cli "claude -p"`)
	fs.StringVar(&bf.baseURL, "base-url", "", "override provider API base URL (e.g. local OpenAI-compatible server)")
	fs.StringVar(&bf.apiKeyEnv, "api-key-env", "", "env var to read the API key from (default: provider's standard var)")
	fs.StringVar(&bf.toolTags, "tool-tags", "", "call tools by XML tags instead of the native API: hermes (for open-weight models behind --base-url)")
	fs.StringVar(&bf.config, "config", "", "path to JSON config")
	fs.StringVar(&bf.agent, "agent", "", "agent name (requires --config)")
	fs.StringVar(&bf.system, "system", "", "system prompt (ad-hoc mode)")
	fs.StringVar(&bf.think, "think", "", "thinking mode: off|auto|budget|disabled (default: the persona's, else the provider's)")
	fs.IntVar(&bf.thinkBudget, "think-budget", 0, "thinking token budget for --think budget")
	fs.StringVar(&bf.effort, "effort", "", "reasoning effort: low|medium|high|xhigh|max (default: the provider's)")
	fs.IntVar(&bf.maxTokens, "max-tokens", 0, "output ceiling, shared by the reply and any thinking")
	fs.IntVar(&bf.maxSteps, "max-steps", 0, "maximum provider calls in one run")
	fs.IntVar(&bf.maxTotalTokens, "max-total-tokens", 0, "cumulative token ceiling for one run (0: none)")
	fs.Var(&bf.skills, "skills", "directory of skill bundles (repeatable)")
	fs.StringVar(&bf.memory, "memory", "", "directory holding persistent notes (MEMORY.md, USER.md) the agent carries between conversations")
	fs.StringVar(&bf.workspace, "workspace", "", "directory the file tools may touch, and the only one")
	fs.BoolVar(&bf.readOnly, "read-only", false, "withhold the file-writing tools from the workspace")
	fs.BoolVar(&bf.fetch, "fetch", false, "allow the agent to retrieve public http/https URLs")
	fs.BoolVar(&bf.fetchInternal, "fetch-internal", false, "also allow private, loopback and link-local addresses (implies --fetch)")
	fs.Var(&bf.allowRun, "allow-run", "program the agent may execute, by name (repeatable)")
	fs.BoolVar(&bf.ask, "ask", false, "confirm at the terminal before any tool that writes, runs a program, reaches the network, or declares nothing")
	fs.StringVar(&bf.audit, "audit", "", "append every tool decision to this file as JSON lines")
	fs.StringVar(&bf.logLevel, "log", "", "log the run to stderr as JSON: debug|info|warn|error")
	fs.StringVar(&bf.session, "session", "", "conversation to resume or start under this id (persisted)")
	fs.StringVar(&bf.store, "store", "", "directory holding sessions (default: $GOLM_SESSIONS or ~/.local/share/golm/sessions)")
	fs.DurationVar(&bf.timeout, "timeout", 0, "overall deadline, e.g. 90s or 2m (0: none)")
	fs.Var(&bf.modalities, "modality", "request non-text output: audio | image (repeatable)")
	fs.BoolVar(&bf.debug, "debug", false, "dump provider HTTP traffic to stderr (credentials redacted)")
	return bf
}

func (bf *backendFlags) debugClient(stderr io.Writer) *http.Client {
	if !bf.debug {
		return nil
	}
	return httpdebug.NewClient(stderr)
}

func (bf *backendFlags) resolveConfigPath(stderr io.Writer) string {
	cfgPath := bf.config
	adHoc := bf.cli != "" || bf.model != "" || bf.baseURL != "" || bf.apiKeyEnv != ""
	if cfgPath == "" && (bf.agent != "" || !adHoc) {
		if p, ok := discoverConfigPath(); ok {
			cfgPath = p
		}
	}
	if cfgPath != "" {
		warnIgnoredConfigFlags(stderr, bf.cli, bf.provider, bf.baseURL, bf.apiKeyEnv, bf.toolTags)
	}
	return cfgPath
}

func (bf *backendFlags) runContext() (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	return bf.withTimeout(ctx, stop)
}

func (bf *backendFlags) sessionContext() (context.Context, <-chan struct{}, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	out := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-sig:
				select {
				case out <- struct{}{}:
				default:
				}
			case <-done:
				return
			}
		}
	}()
	ctx, cancel := bf.withTimeout(ctx, stop)
	return ctx, out, func() {
		signal.Stop(sig)
		close(done)
		cancel()
	}
}

func (bf *backendFlags) withTimeout(ctx context.Context, stop context.CancelFunc) (context.Context, context.CancelFunc) {
	if bf.timeout > 0 {
		tctx, cancel := context.WithTimeout(ctx, bf.timeout)
		return tctx, func() { cancel(); stop() }
	}
	return ctx, stop
}

func exitErr(stderr io.Writer, ctx context.Context, err error) int {
	if errors.Is(err, context.Canceled) && errors.Is(ctx.Err(), context.Canceled) {
		return 130
	}
	fmt.Fprintln(stderr, "golm:", err)
	return 1
}

func warnIgnoredConfigFlags(stderr io.Writer, cliCmd, providerType, baseURL, apiKeyEnv, toolTags string) {
	var ignored []string
	if cliCmd != "" {
		ignored = append(ignored, "--cli")
	}
	if providerType != "" && providerType != "anthropic" {
		ignored = append(ignored, "--provider")
	}
	if baseURL != "" {
		ignored = append(ignored, "--base-url")
	}
	if apiKeyEnv != "" {
		ignored = append(ignored, "--api-key-env")
	}

	if toolTags != "" {
		ignored = append(ignored, "--tool-tags (set tool_tags on the provider instead)")
	}
	if len(ignored) > 0 {
		fmt.Fprintf(stderr, "golm: warning: %s ignored in --config mode (config defines the provider)\n", strings.Join(ignored, ", "))
	}
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func splitArgs(s string) []string { return build.SplitArgs(s) }

func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func defaultKeyEnv(providerType string) string {
	if env := golm.DefaultKeyEnvForProvider(providerType); env != "" {
		return env
	}
	return "ANTHROPIC_API_KEY"
}
