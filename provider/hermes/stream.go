// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package hermes

import (
	"context"
	"strings"

	"github.com/leelsey/golm"
)

// Stream is Complete with the tag traffic taken out of the visible text.
func (p *Provider) Stream(ctx context.Context, req golm.Request, fn func(golm.StreamEvent) error) (golm.Response, error) {
	hadTools := len(req.Tools) > 0
	if !hadTools {
		return p.inner.Stream(ctx, p.prepare(req), fn)
	}
	sc := &scanner{emit: fn, turn: turnOf(req)}
	resp, err := p.inner.Stream(ctx, p.prepare(req), func(ev golm.StreamEvent) error {
		if ev.Type != golm.EventTextDelta {
			return fn(ev)
		}
		return sc.feed(ev.Text)
	})
	if ferr := sc.finish(); err == nil {
		err = ferr
	}
	if err != nil {
		return resp, err
	}

	if resp.Message.Text() == "" && sc.seen.Len() > 0 {
		resp.Message = golm.AssistantText(sc.seen.String())
	}
	return p.interpret(resp, true, turnOf(req)), nil
}

type scanner struct {
	emit func(golm.StreamEvent) error

	pending strings.Builder

	seen strings.Builder

	inCall bool
	calls  int

	turn int
}

func (s *scanner) feed(text string) error {
	if text == "" {
		return nil
	}
	s.seen.WriteString(text)
	s.pending.WriteString(text)
	return s.drain(false)
}

func (s *scanner) finish() error {
	if err := s.drain(true); err != nil {
		return err
	}
	rest := s.pending.String()
	s.pending.Reset()
	if s.inCall {
		rest = callOpen + rest
		s.inCall = false
	}
	return s.text(rest)
}

func (s *scanner) drain(final bool) error {
	for {
		buf := s.pending.String()
		if s.inCall {
			j := strings.Index(buf, callClose)
			if j < 0 {
				if len(buf) > maxBufferedTag {
					s.pending.Reset()
					s.inCall = false
					if err := s.text(callOpen + buf); err != nil {
						return err
					}
					continue
				}
				return nil
			}
			body := buf[:j]
			s.setPending(buf[j+len(callClose):])
			s.inCall = false
			if err := s.call(body); err != nil {
				return err
			}
			continue
		}
		i := strings.Index(buf, callOpen)
		if i >= 0 {
			if err := s.text(buf[:i]); err != nil {
				return err
			}
			s.setPending(buf[i+len(callOpen):])
			s.inCall = true
			continue
		}

		keep := 0
		if !final {
			keep = partialTagLen(buf, callOpen)
		}
		if keep >= len(buf) {
			return nil
		}
		out := buf[:len(buf)-keep]
		s.setPending(buf[len(buf)-keep:])
		if err := s.text(out); err != nil {
			return err
		}
		return nil
	}
}

func (s *scanner) setPending(v string) {
	s.pending.Reset()
	s.pending.WriteString(v)
}

func (s *scanner) text(v string) error {
	if v == "" {
		return nil
	}
	return s.emit(golm.StreamEvent{Type: golm.EventTextDelta, Text: v})
}

func (s *scanner) call(body string) error {
	use, ok := parseCall(body, s.turn, s.calls)
	if !ok {
		return s.text(callOpen + body + callClose)
	}
	s.calls++
	if err := s.emit(golm.StreamEvent{
		Type: golm.EventToolStart, ToolID: use.ID, ToolName: use.Name,
	}); err != nil {
		return err
	}
	if err := s.emit(golm.StreamEvent{
		Type: golm.EventToolDelta, ToolID: use.ID, ToolArgs: string(use.Input),
	}); err != nil {
		return err
	}
	return s.emit(golm.StreamEvent{Type: golm.EventToolStop, ToolID: use.ID})
}

func partialTagLen(s, tag string) int {
	max := len(tag) - 1
	if len(s) < max {
		max = len(s)
	}
	for n := max; n > 0; n-- {
		if strings.HasPrefix(tag, s[len(s)-n:]) {
			return n
		}
	}
	return 0
}
