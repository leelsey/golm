// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/provider/hermes"
)

var providerWrappers = map[string]string{
	"hermes": "wraps another Provider to translate XML tool-call tags; it has no wire, " +
		"no usage of its own and no vendor stop reasons to map",
}

type spy struct {
	caps golm.Capabilities
	resp golm.Response
	err  error
}

func (s *spy) Name() string                    { return "spy" }
func (s *spy) Capabilities() golm.Capabilities { return s.caps }

func (s *spy) Complete(context.Context, golm.Request) (golm.Response, error) {
	return s.resp, s.err
}

func (s *spy) Stream(_ context.Context, _ golm.Request, fn func(golm.StreamEvent) error) (golm.Response, error) {
	if s.resp.Message.Text() != "" {
		if err := fn(golm.StreamEvent{Type: golm.EventTextDelta, Text: s.resp.Message.Text()}); err != nil {
			return golm.Response{}, err
		}
	}
	return s.resp, s.err
}

// TestWrappersDelegateFaithfully is what makes the providerWrappers exemption earned rather than declared.
func TestWrappersDelegateFaithfully(t *testing.T) {
	want := golm.Usage{
		InputTokens: 1234, OutputTokens: 567, ThinkingTokens: 89,
		CacheReadTokens: 4321, CacheWriteTokens: 21, LostCompletions: 2,
	}
	wrapped := []struct {
		name string
		make func(golm.Provider) golm.Provider
	}{
		{"hermes", func(p golm.Provider) golm.Provider { return hermes.Wrap(p) }},
	}
	if len(wrapped) != len(providerWrappers) {
		t.Fatalf("%d wrappers exercised but %d exempted — every exemption must be earned here",
			len(wrapped), len(providerWrappers))
	}

	for _, w := range wrapped {
		t.Run(w.name, func(t *testing.T) {
			if _, ok := providerWrappers[w.name]; !ok {
				t.Fatalf("%q is exercised here but not listed as a wrapper", w.name)
			}
			for _, stop := range []golm.StopReason{
				golm.StopEndTurn, golm.StopMaxTokens, golm.StopRefusal,
				golm.StopContextOverflow, golm.StopPause, golm.StopOther,
			} {
				inner := &spy{
					caps: golm.Capabilities{Streaming: true, Thinking: true, Images: true, PromptCaching: true},
					resp: golm.Response{
						ID: "resp_1", Message: golm.AssistantText("plain answer"),
						StopReason: stop, Usage: want,
					},
				}
				p := w.make(inner)

				req := golm.Request{Tools: []golm.ToolDef{{
					Name: "t", Schema: json.RawMessage(`{"type":"object"}`),
				}}}

				got, err := p.Complete(context.Background(), req)
				if err != nil {
					t.Fatalf("Complete: %v", err)
				}
				if got.Usage != want {
					t.Errorf("Complete usage = %+v, want the wrapped provider's %+v", got.Usage, want)
				}
				if got.StopReason != stop {
					t.Errorf("Complete stop = %q, want %q", got.StopReason, stop)
				}
				if got.ID != "resp_1" {
					t.Errorf("Complete dropped the response id: %q", got.ID)
				}

				got, err = p.Stream(context.Background(), req, func(golm.StreamEvent) error { return nil })
				if err != nil {
					t.Fatalf("Stream: %v", err)
				}
				if got.Usage != want {
					t.Errorf("Stream usage = %+v, want %+v", got.Usage, want)
				}
				if got.StopReason != stop {
					t.Errorf("Stream stop = %q, want %q", got.StopReason, stop)
				}
			}

			inner := &spy{caps: golm.Capabilities{Streaming: true}}
			caps := w.make(inner).Capabilities()
			if caps.Streaming != inner.caps.Streaming {
				t.Errorf("Streaming = %v, want the wrapped provider's %v", caps.Streaming, inner.caps.Streaming)
			}
			if caps.Thinking || caps.Images || caps.PromptCaching {
				t.Errorf("wrapper claims capabilities the wrapped provider does not have: %+v", caps)
			}

			boom := errors.New("wire failed")
			if _, err := w.make(&spy{err: boom}).Complete(context.Background(), req0()); !errors.Is(err, boom) {
				t.Errorf("Complete err = %v, want the wrapped provider's", err)
			}
			if _, err := w.make(&spy{err: boom}).Stream(context.Background(), req0(), func(golm.StreamEvent) error { return nil }); !errors.Is(err, boom) {
				t.Errorf("Stream err = %v, want the wrapped provider's", err)
			}
		})
	}
}

func req0() golm.Request {
	return golm.Request{Tools: []golm.ToolDef{{Name: "t", Schema: json.RawMessage(`{"type":"object"}`)}}}
}
