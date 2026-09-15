// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"io"

	"github.com/leelsey/golm"
)

func usage(w io.Writer) {
	fmt.Fprintf(w, `golm %s — lightweight LLM agent

Usage:
  golm [flags] "prompt"            one-shot prompt (ad-hoc or --config)
  golm tui [flags]                 interactive terminal session
  golm tui --orchestrate --config f  interactive session driving a whole team
  golm mcp-server [--config f]     serve configured agents over MCP (stdio)
  golm a2a-serve [--config f] [--token-env V]  serve an agent over A2A (HTTP)
  golm bus [--addr host:7777] [--token-env V]  run a network-bus hub
  golm serve [--config f] [--addr host:8000]  serve agents as OpenAI-compatible models
  golm acp [--config f]            serve an agent to an editor over ACP (stdio)
  golm config <subcmd>             manage config (init/show/edit/add-*/rm-*/set-default)
  golm sessions <subcmd>           stored conversations (list/show/search/lineage/rm)
  golm --version, -V
  golm --help, -h

Flags (run / tui):
  --model, -m <model>   model name
  --provider <name>     anthropic | openai | google (default: inferred from --model)
  --cli "<cmd>"         external agentic CLI as the model, e.g. "claude -p" (no API key)
  --base-url <url>      override provider base URL (e.g. http://localhost:11434/v1)
  --api-key-env <name>  env var to read the API key from (default: provider's standard var)
  --config <path>       JSON config file
  --agent <name>        agent defined in --config
  --system <text>       system prompt (ad-hoc mode)
  --think <mode>        off | auto | budget | disabled
  --think-budget <n>    thinking tokens for --think budget
  --effort <rung>       reasoning effort: low | medium | high | xhigh | max
  --max-tokens <n>      output ceiling, shared by the reply and any thinking
  --max-steps <n>       maximum provider calls in one run
  --max-total-tokens <n> cumulative token ceiling for one run
  --skills <dir>        directory of skill bundles (repeatable; searched ahead of
                        ~/.config/golm/skills, which is loaded automatically)
  --memory <dir>        persistent notes (MEMORY.md, USER.md) carried between conversations
  --tool-tags <kind>    call tools by XML tags instead of the native API: hermes
  --log <level>         log the run to stderr as JSON: debug|info|warn|error

Built-in tools (all off unless asked for):
  --workspace <dir>     let the agent read and write files, in this directory only
  --read-only           withhold the file-writing tools from the workspace
  --fetch               let the agent retrieve public http/https URLs
  --fetch-internal      also allow private, loopback and link-local addresses
  --allow-run <program> a program the agent may execute, by name (repeatable)
  --ask                 confirm at the terminal before any tool that writes, runs a
                        program, reaches the network, or declares no capabilities
  --audit <file>        append every tool decision to this file as JSON lines
  --image <file>        attach an image as input (repeatable)
  --audio <file>        attach audio as input (repeatable)
  --modality <name>     request non-text output: audio | image (repeatable)
  --no-stream           disable streaming output
  --usage               print token usage to stderr (run: after the run; tui: each turn)
  --debug               dump provider HTTP traffic to stderr (credentials redacted)
  --orchestrate         multi-agent orchestration from --config (see "orchestration")
  --timeout <dur>       overall deadline, e.g. 90s or 2m (default: none)
  --session <id>        resume or start this conversation, persisted between runs
  --store <dir>         where sessions are kept (default: $GOLM_SESSIONS or
                        ~/.local/share/golm/sessions)

Exit status: 0 ok, 1 error, 2 usage, 3 answer cut off at the output ceiling,
130 interrupted.

The workspace is confined with os.Root, so a path that escapes it — including
one through a symlink — is refused by the kernel rather than by a string check.
write_file creates and refuses to overwrite; edit_file replaces one exact,
unambiguous piece of text. --fetch-internal reaches cloud metadata and anything
else only this host can see: turn it on knowingly.

Skills are indexed into the system prompt by name and description; the model
reads a body with the read_skill tool only when it needs one. Your own library at
~/.config/golm/skills is loaded without being asked for ($GOLM_SKILLS_DIR moves
it). A PROJECT's skills are not: a skill is instructions that go into the system
prompt, and loading them from whatever directory you happen to be standing in
would let a cloned repository put words in your agent's mouth. Name them in its
config or pass --skills, which is a decision rather than a side effect. Configured MCP
servers are described from a cached manifest and started only when the model
calls one of their tools. --memory adds a tier below both: MEMORY.md and USER.md
go at the head of the prompt and the agent adds to MEMORY.md with the remember
tool, so what it learned survives the conversation.

--ask puts a gate in front of the tools that touch the world — anything that
writes, runs a program or reaches the network, and anything that declines to say
which. It asks at the terminal, once per call or once per tool if you answer
"always". With no terminal to ask at there is nobody to ask, so those calls are
refused rather than allowed; a config file can describe the same rules in more
detail under "policy". --audit records every decision, the allowed ones
included, because a record of refusals only is not a record.

--tool-tags hermes makes tool calling work against an endpoint that has none:
the schemas go into the prompt as <tools> and the model's <tool_call> tags are
read back out of its answer. It is for open-weight models behind --base-url;
hosted providers want the native API and should leave it unset.

--orchestrate runs a team defined in the config, in one-shot or in golm tui.
Sub-agents keep one session per conversation, so asking one twice continues the
exchange rather than starting a stranger, and --session/--store persist the
WHOLE conversation — the main transcript and every sub-agent's — so resuming
brings the specialists' memory back with it. --max-total-tokens becomes the
shared ceiling covering every delegated agent; the per-agent flags (--model,
--system, --think, --effort, --max-tokens, --max-steps) are the personas' own
and are refused with a warning.

The config's "orchestration" block adds deterministic routing —
"routes" send a matching turn straight to an agent, spending no tokens deciding,
and "route_to_last" keeps a follow-up with whoever answered before it — plus
"fan_out" (one call carrying a list of independent tasks) and "stream_delegates"
(a sub-agent's own output, attributed). A rule naming an agent nobody defined is
refused when the config loads, not at the first turn that matches it.

golm serve exposes every configured agent over the OpenAI Chat Completions API,
which is the surface with no adoption cost: chat front ends, editor plugins and
the openai SDKs all speak it already, so an agent is reachable by changing one
base URL. Each persona is a "model" — model: "researcher" selects it — and with
--orchestrate the whole team is served under one more name, routing and
delegating inside. What crosses the boundary is an AGENT: the tools, skills,
memory, policy gate and sub-agents all run on this side and the caller sees one
assistant message. A request that offers its own tools is refused rather than
answered with prose, because a client waiting for tool calls cannot tell the
difference. Send "golm_session": "<id>" to keep a conversation server-side,
which is the only way an agent's state — sub-agent transcripts, typed state,
compaction lineage — survives between calls.

golm acp serves an agent to an editor — Zed, JetBrains, Neovim, Emacs — over the
Agent Client Protocol. The editor runs it as a subprocess and speaks JSON-RPC on
stdio, so nothing may print to stdout there; every diagnostic goes to stderr.

Two things make it more than another transport. The tool gate is answered by the
person IN the editor, which is the one served surface where a human is present —
so a policy that asks is useful here and refuses everywhere else, and with no
policy configured the --ask rules are applied, because there is finally somebody
to ask. And the file tools go THROUGH the editor: the agent reads the buffer the
person is looking at, unsaved changes included, and writes land where they can be
reviewed and undone rather than on a file that has already moved on.

In an interactive session, write //text to send a message that starts with a
slash: a routing rule may use a "/command" convention of its own, and the
session would otherwise swallow it as an unknown command.

Ctrl-C stops the TURN, not the session — the
conversation and the tokens already spent are worth more than the answer being
given up on. Press it twice to leave, or once at an idle prompt.

Every flag can also be set from the environment: --max-total-tokens reads
GOLM_MAX_TOTAL_TOKENS, --fetch reads GOLM_FETCH, and so on. A flag given on the
command line always wins over the variable. Repeatable flags take a
comma-separated list (GOLM_SKILLS=/a,/b); booleans take true/false, 1/0, yes/no
or on/off. A variable that cannot be parsed is an error naming it.

A subcommand's flags carry its name, so nothing collides: GOLM_BUS_ADDR and
GOLM_A2A_ADDR are separate listeners, GOLM_MCP_NAME is the MCP server's name.
The config-editing subcommands read NO variables at all — their flags say what
to write, and a variable quietly supplying a provider's name is how the wrong
thing ends up in the file.

Two variables are not a plain mapping: --store reads GOLM_SESSIONS, which is
also where the default store lives, and --config is left to discovery below
rather than being set directly, so a stray GOLM_CONFIG cannot take over an
ad-hoc run.

Note: single-dash forms (-config) are still accepted.
Config search (when --config omitted): $GOLM_CONFIG, ./golm.json, ~/.config/golm/config.json
Environment (API keys): ANTHROPIC_API_KEY, OPENAI_API_KEY, GEMINI_API_KEY
Environment (paths): GOLM_CONFIG, GOLM_SESSIONS, GOLM_CACHE, GOLM_SKILLS_DIR

a2a-serve, bus and serve listen on 127.0.0.1 by default. None of them serves
TLS, so a bearer token given to any crosses the network in clear: bind an
interface explicitly only behind a TLS proxy.
`, golm.Version)
}
