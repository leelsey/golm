# GoLM

A lightweight, embeddable **LLM agent framework** in Go — zero third-party
dependencies, fast, and usable as a library, a CLI, or an MCP / A2A server.

> Personal project under **Leelsey**. Licensed under the [MIT Licence](LICENSE).

## Why

- **Embeddable first.** Import `github.com/leelsey/golm` and drive agents from
  your own Go program; the CLI is just a thin wrapper around the same library.
- **Zero dependencies in practice.** The whole framework — HTTP clients, SSE
  parsing, agent loop, config — is pure Go standard library. The one exception is
  `a2a/grpc`, which needs grpc/protobuf; because nothing else imports it, module
  graph pruning keeps those out of *your* build unless you import that package
  yourself (`go mod why google.golang.org/grpc` reports "does not need").
- **Multi-provider.** OpenAI, Anthropic and Google are supported through one
  neutral interface, plus a generic adapter that shells out to agentic CLIs
  (`codex`, `claude`, `agy`).

## Requirements

- Go 1.27 or newer

## Install

```sh
# As a library
go get github.com/leelsey/golm

# The CLI
go install github.com/leelsey/golm/cmd/golm@latest
```

## Library usage

```go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/provider/anthropic"
)

func main() {
	agent := &golm.Agent{
		Provider: anthropic.New(os.Getenv("ANTHROPIC_API_KEY")),
		Model:    "claude-sonnet-4-6",
		System:   "You are concise.",
	}
	res, _ := agent.Run(context.Background(), golm.NewSession(), "Say hello in one word.")
	fmt.Println(res.Text())
}
```

Register tools and the agent will run the reason-act-observe (ReAct) loop,
executing tools until the model produces a final answer — see
[`examples/embed`](examples/embed/main.go).

**From a config, in one call.** A host program gets exactly what the CLI gets —
the provider, the persona's settings and bounds, the skills index folded into
the prompt, the MCP and A2A tools — without hand-wiring any of it:

```go
rt, err := build.New(ctx, build.Options{Config: cfg, Agent: "main", CacheDir: cache})
if err != nil { return err }
defer rt.Close()

rt.Agent.Tools.Register(myOwnTool)          // the host's tools sit alongside
res, err := rt.Agent.Run(ctx, session, "…") // one agent, many sessions
```

`Close` matters even when nothing looks open: a tool may have started a
subprocess. `rt.Tools` is everything the deployment has, `rt.Skills` the loaded
library, `rt.Remotes` the MCP servers and whether any was ever connected.

Stream tokens as they arrive:

```go
agent.Stream(ctx, "Tell me a joke", func(ev golm.StreamEvent) error {
	if ev.Type == golm.EventTextDelta {
		fmt.Print(ev.Text)
	}
	return nil
})
```

## CLI usage

```sh
# Ad-hoc: the model name says which API it speaks, so --provider is optional
ANTHROPIC_API_KEY=... golm --model claude-sonnet-4-6 "Explain goroutines briefly"
OPENAI_API_KEY=...    golm --model gpt-5.2 "Explain goroutines"
GEMINI_API_KEY=...    golm --model gemini-2.5-flash "Explain goroutines"
# A name golm does not recognise is an error asking for --provider, not a guess

# External agentic CLI as the brain — no API key, no config (uses your CLI login)
golm --cli "claude -p --model haiku" "Explain goroutines briefly"
echo "summarise this" | golm --cli "claude -p"

# Local / custom OpenAI-compatible server (Ollama, vLLM) — no config file
golm --base-url http://localhost:11434/v1 --model llama3.1 "Explain goroutines"

# An open-weight model whose server has no tool-calling API: put the schemas in
# the prompt as <tools> and read the model's <tool_call> tags back out
golm --base-url http://localhost:8000/v1 --model Hermes-4-70B --tool-tags hermes \
     --workspace ./project "what does the build do?"
# Custom key from a named env var (value stays out of argv and shell history)
export VLLM_TOKEN=...   # set from a file / secret manager, not typed literally
golm --provider openai --base-url http://localhost:8000/v1 --api-key-env VLLM_TOKEN --model my-model "hi"

# Piped input
echo "summarise this" | golm --model claude-sonnet-4-6

# Config-driven personas
golm --config examples/golm.json --agent main "Plan a release"

# Flags
golm --think auto --model claude-sonnet-4-6 "Solve this step by step"
golm --no-stream --model claude-sonnet-4-6 "..."
golm --timeout 90s --model claude-sonnet-4-6 "..."   # overall deadline (0: none)
golm --usage --model claude-sonnet-4-6 "..."         # print token usage to stderr
golm --debug --model claude-sonnet-4-6 "..."         # dump HTTP traffic (keys redacted)
golm --version

# Bounds on what one run may spend
golm --max-steps 12 -m claude-sonnet-4-6 "..."             # provider calls in one run
golm --max-tokens 4096 -m claude-sonnet-4-6 "..."          # ceiling on one reply
golm --max-total-tokens 200000 -m claude-sonnet-4-6 "..."  # cumulative, whole run

# Reasoning: a mode with a budget, or a rung where that is what the API takes
golm --think budget --think-budget 8192 -m claude-sonnet-4-6 "..."
golm --effort high -m gpt-5.2 "..."          # low | medium | high | xhigh | max

# A durable account of the run, as JSON on stderr
golm --log info -m claude-sonnet-4-6 "..."   # debug | info | warn | error

# Multimodal input (attach files; repeatable)
golm --provider openai --model gpt-5.2 --image photo.png "What is in this image?"
golm --provider google --model gemini-2.5-flash --audio clip.wav "Transcribe this"
# Request non-text OUTPUT with --modality (the provider must support it); any
# returned image/audio is saved to golm-*.* files.
golm --provider openai --model gpt-4o-audio-preview --modality audio "Say hello aloud"

# Interactive terminal session (TUI), and help
golm tui --config golm.json --agent main    # /usage shows token usage, /help lists commands
golm tui --usage -m claude-sonnet-4-6       # print usage after each turn
golm tui --debug -m claude-sonnet-4-6       # wire dump; /debug toggles a per-turn step/tool trace
golm --help

# Conversations that outlive the process
golm --session review -m claude-sonnet-4-6 "start here"   # resumes or starts "review"
golm --session review -m claude-sonnet-4-6 "and then?"    # same conversation, appended
golm tui --session review -m claude-sonnet-4-6            # continue it interactively
golm sessions list                          # ID, updated, message count, title
golm sessions show review                   # the transcript
golm sessions search "rate limit"           # across every stored conversation
golm sessions lineage review                # back through every compaction
golm sessions rm review
```

Nothing is written to disk unless `--session` or `--store` says so. Sessions live
in `$GOLM_SESSIONS`, else `~/.local/share/golm/sessions`, one 0600 JSON file
each. In the TUI `/compact` summarises the head of a long conversation, archives
the whole of it under a fresh id and carries on — the way out of a full context
window that `/reset` used to be.

Exit status: `0` ok, `1` error, `2` usage, `3` the answer was cut off at the
output ceiling, `130` interrupted. A truncated answer is still printed; the
status is how a script can tell it apart from a whole one. Tool failures, policy
refusals and a compaction that did not run are reported on stderr.

`--max-tokens` is the ceiling on a single reply, shared by the answer and any
thinking that precedes it; an answer cut off against it is still printed, and
exit status `3` is how a script tells it from a whole one. `--max-steps` bounds
the reason-act-observe loop. `--max-total-tokens` bounds that loop too, but in a
way worth knowing exactly: it is checked **before each step**, so it stops a run
that keeps going and cannot cap a call already in flight. A run that finishes in
a single step therefore spends whatever that step costs and still exits `0` —
`--max-tokens` is the per-call ceiling, and the two are not substitutes. Under
`--orchestrate` it is **one ceiling shared** by the main agent and every
sub-agent: a delegation is a tool call, so a per-agent limit would leave the
delegated spend outside it entirely.

`--think` and `--effort` are different knobs, not two spellings of one.
`--think off|auto|budget|disabled` says how the model should reason and
`--think-budget` sizes that in tokens; `--effort low|medium|high|xhigh|max` is
the rung, for the APIs that take a rung instead. Neither is forwarded to a model
that cannot express it — see [Notes & limitations](#notes--limitations-v01), where
an unavailable rung is refused before the call rather than turned into a 400
after it.

`--log debug|info|warn|error` writes the run to stderr as JSON lines: run
boundaries at info, steps and tool calls at debug, refusals and tool failures at
warn, and every model- or tool-produced string bounded. It is the *durable*
account — the event bus delivers without blocking and so drops under load, which
is exactly what a record must not do.

Flags accept GNU-style `--flag`; single-dash (`-flag`) is still honoured, and
`-m` is shorthand for `--model`.

### Environment variables

Every flag can also be set from the environment, so a deployment configures once
and stops repeating itself:

```sh
export GOLM_MODEL=claude-sonnet-4-6
export GOLM_EFFORT=high
export GOLM_WORKSPACE=/srv/project
export GOLM_ALLOW_RUN=git,go          # repeatable flags take a comma-separated list
export GOLM_FETCH=yes                 # true/false, 1/0, yes/no, on/off
golm "what changed since the last tag?"
golm --effort low "and a quick summary"   # the flag wins over GOLM_EFFORT
```

The name is the flag, uppercased, dashes to underscores, prefixed `GOLM_`. **A
flag given on the command line always beats the variable** — the mechanism is
simply that a flag the command line set is never overwritten, so there is no
comparison to get wrong. A variable that cannot be parsed is an error naming
both it and the flag it feeds, rather than a setting that quietly does nothing.

A subcommand's flags carry its name — `GOLM_BUS_ADDR` and `GOLM_A2A_ADDR` are
separate listeners — because a generic flag name and a broad variable are a bad
pair. The config-editing subcommands read no variables at all: their flags say
what to *write*, and a stray `GOLM_NAME` supplying a provider's name is how the
wrong thing ends up in the file.

Two are not a plain flag mapping. `--store` reads `GOLM_SESSIONS`, which is also
where the default store lives, because a second name for one directory would be
worse than the inconsistency. And `--config` is deliberately left out: the
existing `GOLM_CONFIG` is consulted during discovery, *after* the ad-hoc backend
flags, so a machine with it set can still run `golm -m some-model "…"` without
being switched into config mode.

Default API-key env vars in ad-hoc mode: `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`,
`GEMINI_API_KEY`.

## Configuration (JSON)

Providers and agent personas are declared in JSON (see
[`examples/golm.json`](examples/golm.json)). API keys are referenced by env-var
**name** — secrets are never stored in the config file. CLI backends wrap an
external agentic tool:

```json
{
  "name": "claude-cli",
  "type": "cli",
  "command": "claude",
  "args": ["-p"],
  "prompt_via": "stdin"
}
```

**API keys never live in the file.** A provider references either an env-var
name (`api_key_env`) or a command that prints the key (`api_key_cmd`, e.g. a
keychain / secret-manager lookup), so the value stays out of the config, argv,
and shell history:

```json
{ "name": "claude", "type": "anthropic",
  "api_key_cmd": "security find-generic-password -s golm-anthropic -w" }
```

### Managing config from the CLI

When `--config` is omitted, golm searches `$GOLM_CONFIG`, then `./golm.json`,
then `~/.config/golm/config.json`. Manage that file without hand-editing JSON:

```sh
golm config init                       # create a starter config at the default path
golm config add-provider --name claude --type anthropic --api-key-env ANTHROPIC_API_KEY
golm config add-provider --name local  --type openai --base-url http://localhost:11434/v1 --api-key-env OLLAMA_KEY
golm config add-agent --name main --provider claude --model claude-sonnet-4-6 --system "Be concise." --default
golm config set-default main
golm config show | validate | path     # inspect / verify / locate
golm config rm-agent main              # removals are validation-guarded
golm config rm-provider local
golm config edit                       # open $EDITOR; validates on save, refuses if broken
```

Every mutation re-validates referential integrity before writing (0600), and
refuses to save a broken config. With a config in place you can drop `--config`:
`golm "prompt"` uses the discovered file's `default_agent`.

### Bounds, cost and compaction per persona

A persona reaches every knob the library has, so an operator sets the limits
rather than the embedder:

```json
{
  "name": "main", "provider": "claude", "model": "claude-sonnet-4-6",
  "system_sections": ["You are…", "Project context…", "Today is…"],
  "max_steps": 12,
  "max_total_tokens": 200000,
  "tool_timeout": "45s",
  "max_tool_result_bytes": 65536,
  "parallel_tools": true,
  "max_parallel_tools": 4,
  "cache_prompt": true,
  "cache_prompt_ttl": "1h",
  "compaction": { "at_input_tokens": 120000, "keep_last": 12, "model": "claude-haiku-4-5" }
}
```

`system_sections` is the prompt as ordered tiers with a cache breakpoint between
each — stable first, volatile last. `compaction` replaces the blunt `keep_last`
trim with a summary plus a verbatim tail; setting both is refused, because the
agent would ignore one of them. With `--session` the pre-compaction conversation
is archived as its own session and reachable through `golm sessions lineage`.

## Multi-agent orchestration

A main agent delegates to named sub-agents, which run concurrently. Sub-agents
reach the main model as tools; an in-process `Bus` lets you watch every agent.

```go
bus := golm.NewBus()
o := golm.NewOrchestrator(bus)

// One ceiling for the WHOLE conversation. Agent.MaxTotalTokens measures a
// single run, and a delegation is a tool call — so without this the sub-agents,
// which are where an orchestration actually spends, sit outside the bound.
o.Budget = golm.NewBudget(200_000)

o.Add("main", "main", mainAgent)
o.Add("researcher", "sub", researchAgent)
o.WireDelegation(map[string]string{
	"researcher": "Delegate factual research questions to this agent.",
})
out, _ := o.Run(ctx, session, "Research X and summarise.")
```

From the CLI, drive a config-defined team (agent `role` + `description`):

```sh
golm --orchestrate --config examples/golm.json "Plan and research a release"
```

A worked, runnable example is in [`examples/orchestrate`](examples/orchestrate),
and `golm tui --orchestrate --config f` drives the same team interactively.

### Persisting a whole conversation

A conversation's transcript is spread across the main session and one per
sub-agent. Saving only the first leaves the coordinator resuming with a full
memory and its specialists with none:

```go
o.SaveConversation(ctx, store, conv)   // main + every sub-agent
o.LoadConversation(ctx, store, conv)   // restores them from the main session
```

The mapping from agent name to stored id lives in the main session's state, so
one id is enough to find all of them. From the CLI, `--session`/`--store` do it
for you in both one-shot and `tui` mode.

> **A ceiling set after `Add` is not a ceiling.** `Add` hands the budget down by
> copying the pointer, so assigning `o.Budget` afterwards changes the
> orchestrator's idea of its ceiling and nothing else. Use `o.SetBudget(b)` —
> and `o.SetToolPolicy(p)` — to reach agents that are already registered.

### What a sub-agent remembers

By default each sub-agent keeps **one session per conversation** and continues
it every time it is asked. The alternative — a fresh session per call — is
context amnesia by construction: the main agent delegates, reads the answer,
delegates to the same sub-agent again, and the sub-agent has no idea it was ever
asked anything. It re-derives what it already worked out, or asks for
information it was given a moment ago.

```go
o.Scope = golm.ScopeCall   // every call starts a stranger; right for fan-out
```

Calls to the *same* sub-agent within one conversation are therefore serialised —
a session carries one run at a time — while different sub-agents still run at
once. A conversation's transcript is spread across the main session and one per
sub-agent, so a host that persists it needs all of them:

```go
for name, s := range o.Sessions(conv.ID()) { store.Save(ctx, s) }
o.Forget(conv.ID())
```

### Routing without a model

The usual shape is a supervisor: the main model reads the turn, decides which
specialist it is for, and calls a delegation tool. That is a whole provider call,
on the whole transcript, before any work is done — and it is the dominant cost of
a conversation that mostly routes obviously.

A `Router` is consulted **before any model is**, so a turn it places never
reaches the main agent at all:

```go
rules, _ := golm.RouteRules([]golm.RouteRule{
	{Agent: "researcher", Any: []string{"research", "look up"}},
	{Agent: "scanner", Prefix: "/scan"},
	{Agent: "triage", Pattern: `CVE-\d{4}-\d+`},
})
o.Router = golm.RouteToLast(rules)
```

An empty `Route.Agent` falls through to the main agent, which is what makes a
Router safe to add to something that already works: a rule that does not match
changes nothing. `RouteToLast` keeps a follow-up with whoever answered before it
— "and the second one?" carries none of the words the rules match on.

Patterns are Go's RE2: no backtracking, so no rule can be made to run for ever.

In a config file:

```json
"orchestration": {
  "route_to_last": true,
  "routes": [
    {"agent": "scanner", "prefix": "/scan"},
    {"agent": "researcher", "any": ["research", "look up"]}
  ]
}
```

### Splitting work

`ParallelTools` only ever runs whatever tool calls the model happened to emit in
one turn. `Fan` takes the **list**:

```go
results, _ := o.Fan(ctx, "researcher", []string{"file A", "file B", "file C"})
```

Every branch gets its own session whatever the Scope — that is what fan-out
means, and sharing one would both serialise them and let one branch's working
leak into another's answer. Results come back in input order; one failure does
not fail the rest, and a failed branch is reported **in place**, because a list
of eleven answers to twelve questions is how a model reports a gap it never
noticed.

Offer it to the model with `o.AsFanTool("researcher", "")` (a `researcher_each`
tool), or `"fan_out": true` in the orchestration block.

### Watching a delegation

A delegation is one tool call that may take a minute. `EventAgentStart` and
`EventAgentStop` bracket every one of them, always:

```go
o.StreamDelegates = true   // also forward the sub-agent's own text

o.Stream(ctx, conv, prompt, func(ev golm.StreamEvent) error {
	switch ev.Type {
	case golm.EventAgentStart:
		fmt.Printf("↳ %s: %s\n", ev.Agent, ev.Text)
	case golm.EventTextDelta:
		if ev.Depth > 0 {
			return nil // a sub-agent's working, not the answer
		}
		fmt.Print(ev.Text)
	}
	return nil
})
```

`StreamDelegates` is off by default: a consumer that prints every delta would
otherwise interleave a sub-agent's working with the answer. Every event carries
`Agent` and `Depth` so a renderer can attribute, indent or fold it.

## Built-in tools

Nothing here is on by default. An agent that can read the filesystem, reach the
network or start a process does so because a deployment said so.

```sh
golm --workspace ./project -m claude-sonnet-4-6 "what does the build do?"
golm --workspace ./project --read-only -m …          # look, do not touch
golm --fetch -m …                                    # public http/https only
golm --fetch-internal -m …                           # also loopback and private
golm --allow-run git --allow-run go -m …             # exactly these programs

# Confirm at the terminal before anything that writes, runs a program, reaches
# the network, or declines to say what it does. Read-only tools stay silent —
# an approval prompt nobody reads is worse than none.
golm --workspace ./project --allow-run git --ask -m …

# Record every decision, the allowed ones included
golm --workspace ./project --audit ./agent-audit.jsonl -m …
```

### Approval and audit

`--ask` compiles a policy from tool **traits** rather than a list of names, so a
tool added tomorrow — a new MCP server, a skill, a plugin — is gated because of
what it does, not because someone remembered it. A tool that declares no traits
at all is treated as unreviewed and gated too.

The prompt shows the arguments, because *"allow `run`?"* is not a question
anyone can answer and *"allow `run rm -rf /`?"* is. Answer `y`, `n`, or `a` to
allow that tool for the rest of the run.

It asks at the **terminal**, never on the agent's own stdin, which may be a pipe.
Where there is no terminal — a scheduled run, an A2A-served agent, a container —
there is nobody to ask, so gated calls are **refused** and say so. A gate that
cannot ask must not fall open.

A config file describes the same rules in more detail:

```json
"policy": {
  "default": "allow",
  "unreviewed": "ask",
  "deny": ["run"],
  "ask_traits": ["network", "filesystem"],
  "allow": ["read_file", "list_dir", "search"]
}
```

Rules are evaluated most specific first and stop at the first match: deny by
name, ask by name, allow by name, deny by trait, ask by trait, unreviewed,
default. A persona may carry its own block, so a sub-agent can be held to
tighter rules than the agent delegating to it.

| Tool | What it refuses |
|---|---|
| `read_file` | anything outside the workspace, binaries, bytes past the cap |
| `write_file` | overwriting; it creates, and `edit_file` changes |
| `edit_file` | text that is not there, and text that is there twice |
| `list_dir`, `search` | the same confinement; results and walk are bounded |
| `fetch` | non-http schemes, and private addresses unless asked |
| `run` | everything not on the allowlist, and there is no default list |

**The workspace is confined with `os.Root`.** Every path is resolved against a
directory handle, so `..` and a symlink pointing out both fail in the kernel
rather than in a string comparison. An absolute path is refused as out of bounds
rather than reported missing, because "no such file" about a file that exists
sends the model looking for another spelling. It is path confinement, not a
sandbox: a real boundary is whatever `golm.ToolPolicy` puts around execution.

**`write_file` creates and will not overwrite.** An existing file is changed with
`edit_file`, which replaces one exact piece of text and refuses two cases: text
that is not in the file, which means the model is working from a stale idea of
it, and text that appears more than once, which is ambiguous. Picking the first
match is how this class of tool destroys the wrong line. Both write through a
temporary file and a rename, so an interrupted write cannot truncate what was
there.

**`fetch` checks the resolved IP at dial time**, on the first request and on
every redirect, which is what a hostname check cannot do: DNS rebinding resolves
publicly for the check and privately for the connection. Loopback, private,
link-local and unique-local are refused unless `allow_private` is set — and
link-local is where cloud metadata serves instance credentials in plain text to
anything that asks.

**`run` takes an allowlist or it does not exist.** No shell is involved, so an
argument containing a semicolon is an argument containing a semicolon. The child
gets a minimal environment rather than the parent's, because the parent's is
where the provider API keys are; name what a tool needs with `inherit_env`.

In a config:

```json
"builtin": {
  "workspace": "/srv/project",
  "fetch": { "allow_private": false, "timeout": "20s" },
  "run": { "allow": ["git", "go"], "inherit_env": ["GOFLAGS"] }
}
```

## Skills

A skill is a directory holding a `SKILL.md` whose front matter names it and says
what it is for:

```markdown
---
name: triage
description: Sort incoming vulnerability reports by severity.
---

# Triage
1. Read the report.
2. Assign a CVSS band.
```

Point golm at one or more libraries — `--skills <dir>` (repeatable) or `skills`
in the config — and only the index reaches the prompt:

```
- triage: Sort incoming vulnerability reports by severity.
```

The model reads a body with the `read_skill` tool when it decides one applies.
Bodies in the prompt would put the whole library in front of the model on every
step of every run, and a library that changed would invalidate the cached prefix
each time. The index sits in a stable prompt tier with a cache breakpoint after
it, so everything before it stays cached. Earlier directories shadow later ones
by name, which is how a project library overrides a user one.

## Memory

The tier between a session and a skill. A session remembers one conversation and
is gone when it ends; a skill is instructions you wrote and the agent may not
change. Memory is what the agent **learned** and should still know next time.

```sh
golm --memory ~/.local/share/golm/memory -m claude-sonnet-4-6 "…"
```

Two documents go at the head of the prompt, in the stable tier behind a cache
breakpoint:

| File | Written by | Why |
|---|---|---|
| `MEMORY.md` | the agent, via `remember` / `forget` | what it worked out and should keep |
| `USER.md` | you | who it is working for — the agent **cannot** edit it |

`USER.md` is read-only to the agent on purpose: one that can rewrite its own
brief can be argued into a different one.

Both are **capped**, and the cap is enforced on the way in. A memory file is in
the system prompt of every step of every run, so an unbounded one is not a large
file — it is a recurring bill that grows. A write that would exceed the cap is
refused, naming the room left, rather than evicting the oldest note: choosing
what to forget is a judgement, and the oldest fact is not the least important
one.

### Where skills come from

`~/.config/golm/skills` is loaded without being asked for (`$GOLM_SKILLS_DIR`
moves it). A **project** directory is not, and the difference is deliberate: a
skill is instructions that go into the system prompt, so auto-loading them from
whatever directory you happen to be standing in would let a cloned repository put
words in your agent's mouth. A project's skills are reachable by naming them in
its config or passing `--skills` — a decision rather than a side effect.

## MCP (Model Context Protocol)

GoLM speaks MCP over stdio in both directions.

**As a server** — expose your configured agents to any MCP client (Claude
Desktop, IDEs, inspectors). stdout carries the protocol; logs go to stderr:

```sh
golm mcp-server --config golm.json
```

**As a client** — consume an external MCP server's tools from your agents by
listing them under `mcp_servers`:

```json
"mcp_servers": [
  { "name": "fs", "command": "mcp-server-filesystem", "args": ["/data"] }
]
```

**Servers start only when they are used.** A tool has to be described before it
can be offered, so "connect on first call" would save nothing — the description
is needed on the first call of every run. GoLM records each server's tool list
in a manifest under `$GOLM_CACHE` (else `~/.cache/golm`), offers the tools from
that, and spawns the subprocess only when the model actually calls one. A
conversation that never touches a server never starts it. Whenever a connection
is made the list is fetched again and the manifest rewritten, so a server whose
tools changed is correct from the next run, and a call to a tool that has since
disappeared fails naming it.

**A server is given a minimal environment, not yours.** An MCP server is a
third-party program — commonly one `npx` fetches from the network at every
start — and your environment is where the provider API keys live. Go hands a
child the whole of `os.Environ` when nothing says otherwise, so the leaking
option is the one you get by writing nothing; GoLM passes enough to run (`PATH`,
`HOME`, `TMPDIR` and their Windows equivalents) and nothing else. A server that
needs its own token is given it **by name**:

```json
"mcp_servers": [
  {
    "name": "github",
    "command": "mcp-server-github",
    "inherit_env": ["GITHUB_PERSONAL_ACCESS_TOKEN"]
  }
]
```

Names, never values — for the same reason a provider carries `api_key_env`
rather than the key. The variable is read from the environment GoLM itself was
started with, so the config file stays safe to commit. Naming one variable does
not open the gate for the rest.

A persona that lists `tools` now resolves those names against the MCP and A2A
tools, which it could not before. Omitting `tools` gives the persona everything
available; `"tools": []` gives it nothing.

Library API: `mcp.NewServer(name, ver).AddRegistry(...)` / `.AddAgent(...)` to
serve, with `.WithPolicy(...)` to gate every `tools/call` through the same
decision point an agent applies to its own loop — a registry exposed over MCP is
otherwise a way round that gate. `mcp.Dial(ctx, cmd, args...)` then
`client.Tools(ctx)` returns `[]golm.Tool` ready to register on an agent;
`mcp.Dialer{Inherit: …}.Dial(...)` names the variables a server may see, and
`Dialer.Env` replaces the environment outright.

`golm mcp-server` serves the **agents** in your config, one tool each. It does
not re-export the tools those agents hold: an MCP client that wanted them would
be reaching past the agent that was configured to use them.

## A2A (Agent2Agent)

GoLM speaks A2A over its JSON-RPC/HTTP binding, both ways.

**As a server** — expose an agent as an A2A agent (Agent Card + `message/send` +
SSE streaming + tasks):

```sh
golm a2a-serve --config golm.json --agent main --addr :8080 --token-env A2A_TOKEN
# card: http://localhost:8080/.well-known/agent-card.json
```

**Authentication is off unless you ask for it.** `--token-env` names an
environment variable holding a bearer token that every RPC must then present
(`Authorization: Bearer …`); the library equivalent is `a2a.Server.AuthToken`.
`--addr :8080` is *every* interface, and without a token anyone who can reach
the port can give the agent work and read any task whose id they can guess — the
command warns on startup when that is the case. The Agent Card stays public
either way: discovery is what a peer does before it holds a credential.

**As a client** — call remote A2A agents from your agents by listing them under
`a2a_agents`:

```json
"a2a_agents": [
  { "name": "researcher", "url": "http://other-host:8080", "description": "deep research",
    "token_env": "RESEARCHER_TOKEN" }
]
```

`token_env` names the variable holding that remote's bearer token — the config
stores the variable name, never the secret, the same way `bus_token_env` does.
Omit it for a remote that serves without authentication. In library code:
`a2a.NewClient(url).WithToken(tok)`.

Library API: `a2a.NewServer(card, a2a.AgentHandler(agent)).HTTPHandler()`;
`a2a.NewClient(url)` with `.SendMessage` / `.SendMessageStream` / `.AsTool`.

**gRPC binding** (`a2a/grpc`, generated from the official `a2a.proto`): same
semantics over gRPC — `grpca2a.Serve(ctx, addr, a2a.AgentHandler(agent))` and
`grpca2a.Dial(addr)` with `.SendMessage` / `.SendMessageStream` / `.AsTool`.
It is the **only** package that pulls grpc/protobuf — the core, MCP, network
bus, HTTP A2A, and the default `golm` binary stay dependency-free.

## Serving agents as an OpenAI model

```sh
golm serve --config golm.json --addr 127.0.0.1:8000 --orchestrate
```

Every other interop layer here needs the far side to learn a protocol. This one
needs it to learn nothing: the OpenAI Chat Completions shape is what chat front
ends, editor plugins and the `openai` SDKs already point at, so an agent is
reachable by changing one base URL.

```python
from openai import OpenAI
client = OpenAI(base_url="http://127.0.0.1:8000/v1", api_key="unused")
client.chat.completions.create(model="researcher", messages=[...])
```

Each persona is a **model**; with `--orchestrate` the whole team is served under
one more name, routing and delegating inside. What crosses the boundary is an
**agent** — the tools, skills, memory, policy gate and sub-agents all run on this
side, and the caller sees one assistant message.

Two deliberate differences from a model endpoint:

- A request that supplies its own `tools` (or `functions`, `tool_choice`, `n>1`)
  is **refused**, not ignored. A client waiting for tool calls and receiving
  prose cannot tell it has been lied to. Sampling parameters *are* accepted and
  ignored — the persona owns them.
- `"golm_session": "<id>"` keeps the conversation server-side. The OpenAI API is
  stateless and its clients resend the history every turn, which works here too —
  but an agent has more state than a transcript, and sub-agent sessions, typed
  state and compaction lineage do not survive being rebuilt from a message list.

`--token-env` adds a bearer token. The listener is loopback by default and
nothing here serves TLS, so exposing it belongs behind a TLS proxy.

## ACP (editors)

```sh
golm acp --config golm.json --agent coder
```

The [Agent Client Protocol](https://agentclientprotocol.com) is what Zed,
JetBrains, Neovim and Emacs speak to a coding agent: the editor runs golm as a
subprocess and drives it over JSON-RPC on stdio. Point the editor's agent
setting at that command and golm appears in its agent panel.

Three things make it more than another transport.

**The gate is answered by a person.** Everywhere else a policy that asks has
nobody to ask, so it refuses. Here the request goes to the editor, which renders
the call and its arguments and sends back what was chosen — so with no policy
configured golm applies the `--ask` rules, because there is finally somebody to
ask. "Always allow this tool" is remembered for the connection.

**The file tools go through the editor.** `read_file` answers from the *buffer*,
so the agent sees what the person is looking at, unsaved changes included; a
write lands in that buffer where it can be reviewed, undone and saved with
everything else. An agent reading the disk instead is reading a file the person
has already moved on from. They are offered only when the editor says it can
answer them.

**Commands run in the editor's terminal.** `--allow-run` over ACP asks the
editor to start the program rather than starting it here, and attaches the live
terminal to the tool call — so a person watches the output as it appears and can
stop it, and can still read it after the agent has moved on. The allowlist is
the same rule as everywhere else: being able to watch a command is not the same
as having agreed to it. What reaches the model is the *tail* of the output,
because a command that fails says why in its last lines. The terminal is
released on every path out, including a turn the editor cancelled.

`--orchestrate` serves the whole team, routing and delegating inside a single
conversation. `--session`/`--store` make `session/load` work, so the editor can
resume a conversation after a restart — and it replays the transcript, because
the editor keeps no copy of its own.

Stdout is the protocol: every diagnostic golm prints there goes to stderr
instead.

## Network bus

Bridge the in-process event `Bus` across processes and machines through a small
central HTTP+SSE hub:

```sh
golm bus --addr :7777          # run the hub
```

Point GoLM instances at it via config (`"bus": "http://hub-host:7777"`): each
process's local `Bus` events are published to the hub and remote events injected
back, with origin tagging to prevent loops. Pure stdlib (no new dependencies).
Library: `netbus.NewHub().Handler()` and
`netbus.NewClient(url).Bridge(ctx, bus, topics...)`.

## Searching past conversations

`sessionstore.Files` keeps a small sidecar beside every session — the listing
metadata plus the transcript's distinct words — so listing reads six fields
instead of a whole transcript, and a search rules a session out without opening
it. Measured over 300 sessions: the sidecars are 0.3% of the transcript bytes
and search is ~50x faster, with byte-identical results.

It is an accelerator, never an authority. Every proposed hit is confirmed
against the session itself, a sidecar that does not match its file is ignored, an
oversized transcript is always read in full, and a store with no sidecars at all
behaves exactly as it did before they existed — which is what makes them safe to
delete. `(*Files).Reindex()` rebuilds them deliberately.

For real query syntax and relevance ranking, [`sessionstore/fts`](sessionstore/fts)
is a SQLite+FTS5 backend in its own module:

```sh
go get github.com/leelsey/golm/sessionstore/fts
```

It is separate so that `go get github.com/leelsey/golm` never pulls a SQLite
driver into a program that does not want one. The driver is pure Go, so it still
cross-compiles without cgo.

## Resilience & lifecycle

Built for long-running embeds and real-world network faults — all standard library:

- **Automatic retries (on by default).** Provider HTTP calls retry transient
  failures (HTTP 429, 5xx, network errors) with exponential backoff + jitter,
  honouring `Retry-After`. Tune or disable per provider with
  [`httpretry`](httpretry/httpretry.go):
  `anthropic.New(key).WithRetry(httpretry.Policy{Attempts: 5})` or `.WithoutRetry()`.
- **Deadlines.** Every request honours its `context`. The CLI's `--timeout` wraps
  the run in a deadline; library callers pass a `context.WithTimeout` or a custom
  `*http.Client` via `WithHTTPClient`. No client-level timeout is set by default,
  so streaming responses are never cut mid-stream.
- **Graceful shutdown, in two phases.** On SIGINT/SIGTERM every served surface
  stops accepting, gives in-flight requests a short grace to finish, and then
  cancels what is left. The second phase is what makes the first one safe to
  have: an SSE stream — a streaming completion, an A2A `message/stream`, a bus
  subscriber — does not end because the server was asked to stop, so waiting for
  it meant waiting the whole shutdown budget and then giving up with the handler
  still running. `a2a-serve` also drains its task store, and `bus` releases its
  subscribers, so both stop in milliseconds rather than at the deadline.
  (Read/idle timeouts are set; no write timeout, so SSE survives while running.)
- **A2A task lifecycle.** The server evicts old/terminal tasks (TTL + size cap),
  and `tasks/cancel` cancels the in-flight handler's context.
- **Self-healing bus bridge.** `netbus` clients reconnect with backoff and resume
  from the last event ID (`Last-Event-ID`) after a hub blip.
- **Bounded teardown.** `mcp.Client.Close()` kills a child that ignores stdin EOF
  rather than hanging.
- **Token accounting & observability.** A run's `Result` reports its cumulative
  `Usage` and its per-provider-call `StepUsage`, and `Session.Usage()` reports
  what the whole conversation has cost; `Usage` carries input/output/thinking
  plus cache read/write token counts (cached tokens are a breakdown of input).
  `Orchestrator.Usage()` aggregates delegated sub-agent usage; the CLI prints
  it all with `--usage` (TUI: `/usage`); `Bus.Dropped()` surfaces events
  dropped to a slow consumer.
- **Debugging.** `httpdebug.NewClient(w)` (or `--debug` on the CLI/TUI) dumps
  every provider HTTP request/response — headers, bodies, live SSE, retries —
  with credentials redacted; wire it into any provider via `WithHTTPClient`.
  `Agent.Bus` publishes typed lifecycle events (`StepEvent` with stop reason
  and usage per provider call, `ToolCallEvent`/`ToolResultEvent` with
  arguments and results) shown by `--orchestrate`'s event log and the TUI's
  `/debug` per-turn trace.
- **Sessions have an identity.** A `Session` carries an id (`crypto/rand.Text`,
  so it is safe as a filename and a path segment), a title, timestamps, a
  `Parent` link to the archive it was last compacted from, and typed state —
  `SetState` marshals at once, so `GetState[int]` returns an `int` whether or not
  the session has been through a store. `SessionStore` addresses sessions by id
  with list, search and delete; `sessionstore.Memory` and `sessionstore.Files`
  implement it in pure stdlib, and `Lineage` walks a compacted conversation back
  through its archives. A full-text backend needs a third-party driver and so
  belongs in its own nested module, never in the core.
- **A decision point before a tool runs.** `Agent.ToolPolicy` is consulted once
  per call, in the model's order, *before* the call's `ToolTimeout` and
  abandonment window start — so an approval gate may block on a human without
  spending the tool's clock or being abandoned by it. Any error denies, and its
  text is what the model is told; the call is still answered, so the transcript
  stays balanced. A policy that panics denies, because a gate that fails open is
  not a gate. `mcp.Server.WithPolicy` gates the second path to `Tool.Execute`, so
  a registry exposed over MCP is not a way round it. The context a policy returns
  is what the tool runs with, which is where a sandbox handle or a capability
  token goes without touching the `Tool` interface. `ToolTraits` are the tool's
  own claim about itself and route a decision; they are not a boundary, and
  `TraitsOf` reports undeclared rather than harmless. The audit record belongs in
  the policy, not on the `Bus`, which drops events under load.
- **Tool results are content, not a string.** `Tool.Execute` returns
  `[]ToolContent` — text, images or audio — so a screenshot or a rendered chart
  can go back to the model. `NewTool`, `TextTool` and `NewTypedTool` still take
  string-returning functions; `NewContentTool` and `NewTypedContentTool` are
  their block-returning siblings. A media result needs
  `Capabilities.ToolResultImages`: an adapter without it fails the request naming
  the tool rather than sending the model a result with the image taken out.
- **An ordered system prompt.** `SystemPrompt` is the prompt as a list of
  sections rather than one string, so a caller with memory, a skills index and
  a timestamp to place can put the stable half first and mark where it ends
  (`Add(...).Break()`). The ORDER pays on every provider — the ones that cache
  prefixes automatically match on a longer one — while the marks need
  `Capabilities.PromptCaching`, which only Anthropic reports today. `Agent.System`
  stays a plain string and leads the assembled prompt.
- **History compaction, destructive or not.** `Session.Trim(keepLast)` and
  `Session.DropBefore(n)` cap unbounded growth by dropping the head outright,
  and `Agent.KeepLast` applies a trim after every run. `Agent.Compaction` is the
  non-destructive form: the head becomes a summary, the conversation as it was
  is handed to `Archive` under a fresh id, and the live session records it as its
  `Parent` — so an external reference stays valid and `Lineage` can read the
  whole thing back. Nothing is dropped until the archive is away, so a failed
  compaction cannot lose a transcript. Both work against `CachePrompt`, but a
  trim moves the prefix after every run where a compaction moves it once.
- **Ceilings, not just meters.** `Agent.MaxTotalTokens` ends a run once its
  cumulative usage reaches the cap (`ErrTokenBudget`), checked before each step
  so it is not exceeded by more than the step that crosses it.
  `Agent.ToolTimeout` bounds one tool call and `Agent.MaxToolResultBytes` bounds
  what a result contributes to the history — a result is re-sent on every later
  step, so an unbounded one is billed once per remaining step. Text is truncated
  with a marker; a media block that does not fit is replaced by a note naming its
  kind and size, because half a PNG is not a smaller PNG. A failing tool's error
  is bounded the same way. A tool that
  ignores its context is abandoned rather than allowed to hold the loop past the
  deadline, and its call is still answered so the transcript stays balanced.
- **One run at a time per session, enforced.** Two runs cannot share one
  transcript, so a second concurrent `Run`/`Stream` on the same `Session`
  returns `ErrSessionBusy`. One configured `Agent` serves any number of
  concurrent conversations, one `Session` each — that is how you fan out.
- **Terminal stops are errors.** A refusal and an exhausted context window come
  back as `ErrRefused` / `ErrContextOverflow` (a `*StopError` carrying the
  terminal `Response`, so partial content and the tokens it cost survive) rather
  than as a nil error with an empty message.

## Project layout

```
golm/
├── golm.go provider.go agent.go message.go content.go tool.go session.go config.go
│   bus.go orchestrator.go   # root package `golm`: public API, ReAct loop, orchestration
├── provider/
│   ├── anthropic/  openai/  google/   # hand-rolled net/http adapters
│   └── clibackend/                    # generic agentic-CLI adapter (os/exec)
├── httpretry/            # retry/backoff policy (public, stdlib)
├── skills/  memory/  tools/           # skill bundles, persistent notes, built-in tools
├── openaiapi/            # serve agents over the OpenAI Chat Completions API
├── build/                # one-call construction of an agent from a config
├── sessionstore/         # Memory and Files backends (Files keeps a per-session index)
│   └── fts/              # SQLite+FTS5 backend — a NESTED module, so `go get golm` never pulls it
├── acp/                  # serve an agent to an editor (Agent Client Protocol)
├── mcp/  a2a/  a2a/grpc/  netbus/     # interop: MCP, A2A (HTTP + gRPC), network bus
├── internal/
│   ├── sse/  rpc/  jsonrpc/   # SSE parser + JSON-RPC transport/types (rpc.Peer is bidirectional)
│   ├── proc/                 # process groups: kill a child WITH what it started
│   └── tui/                  # interactive terminal session (binary-only)
├── cmd/golm/              # thin CLI binary + config subcommands
└── examples/embed/        # embedding golm as a library
```

`provider/hermes` is a **wrapper**, not an adapter: it gives any provider tool
calling against an endpoint that has none, and is exempt from the wire contracts
only because a delegation test proves it passes usage, stop reasons and errors
through untouched.

## Build & test

```sh
go build ./...
go vet ./...
go test ./...
go test -tags live -run TestLive ./...   # live integration: needs the `claude` CLI

make build      # version-stamped binary -> bin/golm
make release    # cross-compiled dist/golm-<os>-<arch>[.exe] + checksums.txt
```

Provider clients are tested against `httptest` servers, so the default suite runs
fully offline. The core, `mcp`, `netbus` and the JSON-RPC/HTTP `a2a` pull no
third-party dependencies; only `a2a/grpc` adds gRPC/protobuf, and it is not linked
into the default `golm` binary.

## Notes & limitations (v0.1)

- OpenAI uses the **Chat Completions** API (stable, well-understood). `reasoning_effort`
  and `temperature` are gated by model family — reasoning models (gpt-5, o1/o3/o4)
  get the former, others the latter — so neither triggers a 400. Reasoning *text* is
  not surfaced by this endpoint; the Responses API is a future enhancement.
- **Effort is per model, and so is its ladder.** `Request.Effort` is only sent
  where the model accepts it, because a model that does not takes the request
  down with a 400 rather than ignoring the field. On Anthropic the rungs differ
  between models that all accept one — `xhigh` arrived with Opus 4.7, and Opus
  4.5 stops at `high` — so an unavailable rung is refused before the call rather
  than after. Ask `anthropic.IsEffortModel` / `openai.IsReasoningModel` instead
  of keeping your own list. A model newer than those tables is forwarded, not
  refused.
- **Multimodal.** Image + audio **input** work across providers (`--image` /
  `--audio`, or `Agent.RunMessage`). Media **output** returned by a model is parsed
  and saved to `golm-*.*`; request it with `--modality` / `Agent.ResponseModalities`
  where the provider supports it. **Anthropic has no audio input** — the CLI warns
  and the attachment is ignored.
- Multi-agent orchestration, MCP (stdio), A2A (HTTP + gRPC) and the network bus are
  all available.

## Licence

MIT © 2026 Leelsey

Third-party material is confined to `a2a/grpc`: the modules in `go.mod` and a
modified copy of the A2A specification's protobuf definition, both recorded in
[`THIRD_PARTY_NOTICES`](THIRD_PARTY_NOTICES). Everything else, the default `golm`
binary included, is standard library only.
