// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

// Package tui is a lightweight, standard-library-only interactive terminal session for the golm CLI.
package tui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/leelsey/golm"
)

// Options configures one interactive session.
type Options struct {
	Agent *golm.Agent

	Orchestrator *golm.Orchestrator
	In           io.Reader
	Out          io.Writer

	OnFinal func(golm.Result)

	Store golm.SessionStore

	Session *golm.Session

	Interrupt <-chan struct{}
}

// Run drives a line-based session until /exit or EOF, colourised only on a terminal.
func Run(ctx context.Context, o Options) error {
	agent, in, out, onFinal := o.Agent, o.In, o.Out, o.OnFinal
	sess := o.Session
	if sess == nil {
		sess = golm.NewSession()
	}
	var run golm.Runner = agent
	if o.Orchestrator != nil {
		run = o.Orchestrator
		if agent == nil {
			agent = o.Orchestrator.Main()
		}
	}
	if agent == nil {
		return errors.New("tui: no agent")
	}
	st := &state{sess: sess, store: o.Store, agent: agent, orc: o.Orchestrator}
	color := isTTY(out)
	fmt.Fprintln(out, paint(color, "1", "golm interactive")+" — /help for commands, /exit to quit")
	if o.Store != nil {
		fmt.Fprintln(out, paint(color, "2", "(session "+st.sess.ID()+", saved after every turn)"))
	}

	createdBus := agent.Bus == nil
	if createdBus {
		agent.Bus = golm.NewBus()
	}
	events, unsub := agent.Bus.Subscribe("*")
	defer func() {
		unsub()
		if createdBus {
			agent.Bus.Close()
			agent.Bus = nil
		}
	}()
	debug := false
	var pending []string

	collect := func() {
		for {
			select {
			case ev, ok := <-events:
				if !ok {
					return
				}
				if l := traceLine(ev); l != "" {
					pending = append(pending, l)
				}
			default:
				return
			}
		}
	}
	dropSeen := agent.Bus.Dropped()
	flushTrace := func() {
		collect()
		if debug {
			for _, l := range pending {
				fmt.Fprintln(out, paint(color, "2", "("+l+")"))
			}
		}
		pending = pending[:0]

		if d := agent.Bus.Dropped(); d != dropSeen {
			if debug {
				fmt.Fprintf(out, "%s\n", paint(color, "2", fmt.Sprintf("(trace incomplete: %d event(s) dropped)", d-dropSeen)))
			}
			dropSeen = d
		}
	}

	lines, errc := readLines(ctx, in)
	for {
		fmt.Fprint(out, "\n"+paint(color, "1;36", "you ▸ "))
		var raw string
		select {
		case <-ctx.Done():
			fmt.Fprintln(out)
			return ctx.Err()
		case <-o.Interrupt:

			fmt.Fprintln(out)
			return nil
		case l, ok := <-lines:
			if !ok {
				if err := <-errc; err != nil && err != io.EOF {
					return err
				}
				return nil
			}
			raw = l
		}
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		if rest, ok := strings.CutPrefix(line, "//"); ok {
			line = "/" + rest
		} else if strings.HasPrefix(line, "/") {
			if command(ctx, st, line, out, color, &debug) {
				return nil
			}
			continue
		}

		fmt.Fprint(out, paint(color, "1;32", "golm ◂ "))

		turnCtx, endTurn := context.WithCancel(ctx)
		turnDone := make(chan struct{})
		quit := watchInterrupt(turnDone, o.Interrupt, endTurn, out, color)
		res, err := run.StreamMessage(turnCtx, st.sess, golm.UserText(line), func(ev golm.StreamEvent) error {
			collect()
			switch ev.Type {
			case golm.EventTextDelta:

				if ev.Depth > 0 {
					fmt.Fprint(out, paint(color, "2", ev.Text))
					break
				}
				fmt.Fprint(out, ev.Text)
			case golm.EventThinkingDelta:
				fmt.Fprint(out, paint(color, "2", ev.Text))
			case golm.EventAgentStart:
				fmt.Fprint(out, paint(color, "2", "\n"+indent(ev.Depth)+"↳ "+ev.Agent+": "+trunc(ev.Text, 70)+"\n"))
			case golm.EventAgentStop:
				fmt.Fprint(out, paint(color, "2", "\n"+indent(ev.Depth)+"↳ "+ev.Agent+" done\n"))
			case golm.EventToolStart:
				fmt.Fprint(out, paint(color, "2", "\n"+indent(ev.Depth)+"(running "+ev.ToolName+"…)\n"))
			}
			return nil
		})
		close(turnDone)
		endTurn()
		fmt.Fprintln(out)
		flushTrace()
		if err != nil {
			interrupted := ctx.Err() == nil && errors.Is(err, context.Canceled)
			if interrupted {
				fmt.Fprintln(out, paint(color, "2", "(interrupted)"))
			} else {
				fmt.Fprintln(out, paint(color, "31", "error: "+err.Error()))
			}

			st.save(ctx, out, color)
			if quit.Load() {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		st.last = res
		st.save(ctx, out, color)
		if onFinal != nil {
			onFinal(res)
		}
	}
}

func watchInterrupt(done <-chan struct{}, in <-chan struct{}, cancel context.CancelFunc, out io.Writer, color bool) *atomic.Bool {
	var second atomic.Bool
	if in == nil {
		return &second
	}
	go func() {
		select {
		case <-done:
			return
		case _, ok := <-in:
			if !ok {
				return
			}
		}
		fmt.Fprintln(out, paint(color, "2", "\n(stopping this turn — interrupt again to exit)"))
		cancel()
		select {
		case <-done:
		case _, ok := <-in:
			if ok {
				second.Store(true)
			}
		}
	}()
	return &second
}

func traceLine(ev golm.Event) string {
	switch d := ev.Data.(type) {
	case golm.StepEvent:
		return fmt.Sprintf("step %d: %s — %s", d.Step, d.StopReason, d.Usage)
	case golm.ToolCallEvent:
		return "tool " + d.Name + " " + trunc(string(d.Input), 120)
	case golm.ToolResultEvent:
		if d.IsError {
			return "tool " + d.Name + " → error: " + trunc(d.Content, 200)
		}
		return "tool " + d.Name + " → " + trunc(d.Content, 200)
	case golm.RouteEvent:
		return "routed to " + d.Agent + ": " + d.Reason
	case string:
		if ev.Kind == "error" {
			return "error: " + trunc(d, 200)
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

func indent(depth int) string {
	if depth <= 0 {
		return ""
	}
	if depth > 4 {
		depth = 4
	}
	return strings.Repeat("  ", depth)
}

const maxLineBytes = 1 << 20

func readLines(ctx context.Context, in io.Reader) (<-chan string, <-chan error) {
	lines := make(chan string)
	errc := make(chan error, 1)
	go func() {
		defer close(lines)
		r := bufio.NewReader(in)
		var sb strings.Builder
		for {
			chunk, err := r.ReadSlice('\n')
			sb.Write(chunk)

			if sb.Len() > maxLineBytes+1 {
				errc <- fmt.Errorf("input line exceeds %d bytes", maxLineBytes)
				return
			}
			if err == bufio.ErrBufferFull {
				continue
			}
			if s := sb.String(); s != "" {
				sb.Reset()
				select {
				case lines <- s:
				case <-ctx.Done():
					errc <- ctx.Err()
					return
				}
			}
			if err != nil {
				errc <- err
				return
			}
		}
	}()
	return lines, errc
}

type state struct {
	sess  *golm.Session
	last  golm.Result
	store golm.SessionStore
	agent *golm.Agent

	orc *golm.Orchestrator
}

func (st *state) save(ctx context.Context, out io.Writer, color bool) {
	if st.store == nil {
		return
	}

	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), saveTimeout)
	defer cancel()

	var err error
	if st.orc != nil {
		err = st.orc.SaveConversation(sctx, st.store, st.sess)
	} else {
		err = st.store.Save(sctx, st.sess)
	}
	if err != nil {
		fmt.Fprintln(out, paint(color, "31", "not saved: "+err.Error()))
	}
}

const saveTimeout = 10 * time.Second

const compactKeepLast = 10

func (st *state) compact(ctx context.Context, out io.Writer, color bool) {
	cfg := golm.CompactConfig{KeepLast: compactKeepLast}
	if p := st.agent.Compaction; p != nil {
		cfg = p.CompactConfig
	}
	if cfg.Summarise == nil {
		cfg.Summarise = golm.SummariseWith(st.agent.Provider, st.agent.Model, "")
	}
	if cfg.Archive == nil && st.store != nil {
		cfg.Archive = func(ctx context.Context, archived *golm.Session) error {
			return st.store.Save(ctx, archived)
		}
	}
	fmt.Fprintln(out, paint(color, "2", "(summarising…)"))
	res, err := st.sess.Compact(ctx, cfg)
	if err != nil {
		fmt.Fprintln(out, paint(color, "31", "compaction failed: "+err.Error()))
		return
	}
	if res.Archive == nil {
		fmt.Fprintln(out, paint(color, "2", "(nothing to compact yet)"))
		return
	}
	fmt.Fprintf(out, "%s\n", paint(color, "2", fmt.Sprintf("(compacted: %d messages summarised, %d kept, archived as %s)", res.Dropped, st.sess.Len(), res.Archive.ID())))
	st.save(ctx, out, color)
}

func command(ctx context.Context, st *state, line string, out io.Writer, color bool, debug *bool) (stop bool) {
	fields := strings.Fields(line)
	switch fields[0] {
	case "/exit", "/quit":
		return true
	case "/reset":

		if st.orc != nil {
			st.orc.Forget(st.sess.ID())
		}
		st.sess, st.last = golm.NewSession(), golm.Result{}
		fmt.Fprintln(out, paint(color, "2", "(session reset)"))
		if st.store != nil {
			fmt.Fprintln(out, paint(color, "2", "(now in session "+st.sess.ID()+")"))
			st.save(ctx, out, color)
		}
	case "/compact":
		st.compact(ctx, out, color)
	case "/session":
		m := st.sess.Meta()
		fmt.Fprintf(out, "%s\n", paint(color, "2", fmt.Sprintf("(id %s, %d messages, updated %s)", m.ID, m.Messages, m.Updated.Format(time.RFC3339))))
		if m.Parent != "" {
			fmt.Fprintln(out, paint(color, "2", "(compacted from "+m.Parent+")"))
		}
		if m.Title != "" {
			fmt.Fprintln(out, paint(color, "2", "(title "+m.Title+")"))
		}
		if st.orc != nil {
			for name, sub := range st.orc.Sessions(m.ID) {
				fmt.Fprintf(out, "%s\n", paint(color, "2", fmt.Sprintf("(  %s: %d messages)", name, sub.Len())))
			}
		}
		if st.store == nil {
			fmt.Fprintln(out, paint(color, "2", "(not persisted — start with --session <id> to keep it)"))
		}
	case "/title":
		title := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
		if title == "" {
			fmt.Fprintln(out, paint(color, "2", "(usage: /title <text>)"))
			break
		}
		st.sess.SetTitle(title)
		st.save(ctx, out, color)
		fmt.Fprintln(out, paint(color, "2", "(title set)"))
	case "/usage":
		fmt.Fprintln(out, paint(color, "2", "(last run: "+st.last.Usage.String()+")"))
		fmt.Fprintln(out, paint(color, "2", "(session:  "+st.sess.Usage().String()+")"))
	case "/debug":
		*debug = !*debug
		if *debug {
			fmt.Fprintln(out, paint(color, "2", "(debug on — per-turn step/tool trace)"))
		} else {
			fmt.Fprintln(out, paint(color, "2", "(debug off)"))
		}
	case "/help":
		fmt.Fprintln(out, paint(color, "2", "commands: /usage  /session  /title  /compact  /debug  /reset  /exit  /help"))
		fmt.Fprintln(out, paint(color, "2", "write //text to send a message that starts with a slash"))
	default:
		fmt.Fprintln(out, paint(color, "2",
			"(unknown command "+fields[0]+" — write //"+strings.TrimPrefix(fields[0], "/")+" to send it as a message)"))
	}
	return false
}

func paint(color bool, code, s string) string {
	if !color {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
