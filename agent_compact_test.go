// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type stubSummariser struct {
	calls int
	head  []Message
	text  string
	usage Usage
	err   error
	gate  chan struct{}
}

func (st *stubSummariser) fn(ctx context.Context, msgs []Message) (Summary, error) {
	st.calls++
	st.head = msgs
	if st.gate != nil {
		select {
		case <-st.gate:
		case <-ctx.Done():
			return Summary{}, ctx.Err()
		}
	}
	if st.err != nil {
		return Summary{}, st.err
	}
	return Summary{Text: st.text, Usage: st.usage}, nil
}

func seeded() *Session {
	s := NewSession()
	s.Append(UserText("u1"), AssistantText("a1"), UserText("u2"), AssistantText("a2"))
	return s
}

func transcript(s *Session) []string {
	h := s.History()
	out := make([]string, len(h))
	for i, m := range h {
		out[i] = m.Text()
	}
	return out
}

func wantTranscript(t *testing.T, s *Session, want ...string) {
	t.Helper()
	got := transcript(s)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("transcript = %q, want %q", got, want)
	}
}

func noopToolReg(t *testing.T, fn func()) *Registry {
	t.Helper()
	r := NewRegistry()
	r.Register(NewTool("t", "does nothing", json.RawMessage(`{"type":"object"}`),
		func(context.Context, json.RawMessage) (string, error) {
			if fn != nil {
				fn()
			}
			return "ok", nil
		}))
	return r
}

func TestCompactionAtMessagesFiresOnRunExit(t *testing.T) {
	st := &stubSummariser{text: "S"}
	p := &recordingProvider{responses: []Response{{Message: AssistantText("done"), StopReason: StopEndTurn}}}
	a := &Agent{Provider: p, Model: "m", Compaction: &CompactPolicy{
		CompactConfig: CompactConfig{KeepLast: 2, Summarise: st.fn},
		AtMessages:    5,
	}}

	s := seeded()
	res, err := a.Run(context.Background(), s, "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.CompactError != nil {
		t.Fatalf("CompactError = %v, want nil", res.CompactError)
	}

	if st.calls != 1 {
		t.Fatalf("summariser called %d times, want 1", st.calls)
	}
	wantTranscript(t, s, SummaryPrefix+"S", "done")

	if len(st.head) != 5 || st.head[0].Text() != "u1" {
		t.Errorf("summarised %d messages starting %q, want 5 starting \"u1\"", len(st.head), st.head[0].Text())
	}
}

func TestCompactionAtMessagesDoesNotFireBelowThreshold(t *testing.T) {
	st := &stubSummariser{text: "S"}
	p := &recordingProvider{responses: []Response{{Message: AssistantText("done"), StopReason: StopEndTurn}}}
	a := &Agent{Provider: p, Model: "m", Compaction: &CompactPolicy{
		CompactConfig: CompactConfig{KeepLast: 2, Summarise: st.fn},
		AtMessages:    10,
	}}

	s := seeded()
	res, err := a.Run(context.Background(), s, "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if st.calls != 0 {
		t.Errorf("summariser called %d times below the threshold, want 0", st.calls)
	}
	if res.CompactError != nil {
		t.Errorf("CompactError = %v; a compaction that was not due is not a failure", res.CompactError)
	}
	wantTranscript(t, s, "u1", "a1", "u2", "a2", "go", "done")
}

// The token threshold reads the LAST provider call, not the largest.
func TestCompactionAtInputTokensReadsTheLastCall(t *testing.T) {
	cases := []struct {
		name        string
		first, last int
		want        bool
	}{
		{"last call over the threshold", 20, 500, true},
		{"only the first call was over", 500, 20, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rs := toolCallThen("done")
			rs[0].Usage = Usage{InputTokens: tc.first}
			rs[1].Usage = Usage{InputTokens: tc.last}

			st := &stubSummariser{text: "S"}
			a := &Agent{
				Provider: &fakeProvider{responses: rs},
				Model:    "m", Tools: noopToolReg(t, nil),
				Compaction: &CompactPolicy{
					CompactConfig: CompactConfig{KeepLast: 2, Summarise: st.fn},
					AtInputTokens: 100,
				},
			}

			s := seeded()
			if _, err := a.Run(context.Background(), s, "go"); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := st.calls > 0; got != tc.want {
				t.Fatalf("compacted = %v, want %v (last call billed %d input tokens)", got, tc.want, tc.last)
			}
			if !tc.want {
				if got := s.Len(); got != 8 {
					t.Errorf("transcript = %d messages, want the 8 it ran to", got)
				}
				return
			}
			if got := transcript(s)[0]; !strings.HasPrefix(got, SummaryPrefix) {
				t.Errorf("first message = %q, want the summary", got)
			}
		})
	}
}

// A compaction is not a step of the run.
func TestCompactionUsageGoesOnlyToTheSession(t *testing.T) {
	step := Usage{InputTokens: 11, OutputTokens: 5}
	st := &stubSummariser{text: "S", usage: Usage{InputTokens: 700, OutputTokens: 30}}
	p := &recordingProvider{responses: []Response{
		{Message: AssistantText("done"), StopReason: StopEndTurn, Usage: step},
	}}
	a := &Agent{Provider: p, Model: "m", Compaction: &CompactPolicy{
		CompactConfig: CompactConfig{KeepLast: 2, Summarise: st.fn},
		AtMessages:    5,
	}}

	s := seeded()
	res, err := a.Run(context.Background(), s, "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if st.calls != 1 {
		t.Fatalf("summariser called %d times, want 1", st.calls)
	}
	if res.Usage != step {
		t.Errorf("Result.Usage = %+v, want the run's steps alone %+v", res.Usage, step)
	}
	if len(res.StepUsage) != 1 || res.StepUsage[0] != step {
		t.Errorf("StepUsage = %+v, want exactly the one step %+v", res.StepUsage, step)
	}
	want := Usage{InputTokens: 711, OutputTokens: 35}
	if got := s.Usage(); got != want {
		t.Errorf("Session.Usage() = %+v, want the run plus the summariser %+v", got, want)
	}
}

func TestCompactionErrorIsReportedAndKeepsTheTranscript(t *testing.T) {
	errBoom := errors.New("summariser down")
	st := &stubSummariser{err: errBoom}
	p := &recordingProvider{responses: []Response{{Message: AssistantText("done"), StopReason: StopEndTurn}}}
	a := &Agent{Provider: p, Model: "m", Compaction: &CompactPolicy{
		CompactConfig: CompactConfig{KeepLast: 2, Summarise: st.fn},
		AtMessages:    5,
	}}

	s := seeded()
	res, err := a.Run(context.Background(), s, "go")
	if err != nil {
		t.Fatalf("Run: %v; a failed compaction must not fail the run", err)
	}
	if !errors.Is(res.CompactError, errBoom) {
		t.Errorf("CompactError = %v, want %v", res.CompactError, errBoom)
	}

	wantTranscript(t, s, "u1", "a1", "u2", "a2", "go", "done")
}

// The summariser is a provider call.
func TestCancelledRunSkipsCompaction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	st := &stubSummariser{text: "S"}
	a := &Agent{
		Provider: &fakeProvider{responses: toolCallThen("done")},
		Model:    "m", Tools: noopToolReg(t, cancel),
		Compaction: &CompactPolicy{
			CompactConfig: CompactConfig{KeepLast: 2, Summarise: st.fn},
			AtMessages:    3,
		},
	}

	s := seeded()
	res, err := a.Run(ctx, s, "go")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run err = %v, want context.Canceled", err)
	}
	if st.calls != 0 {
		t.Errorf("summariser called %d times on a cancelled run, want 0", st.calls)
	}
	if !errors.Is(res.CompactError, context.Canceled) {
		t.Errorf("CompactError = %v, want context.Canceled", res.CompactError)
	}

	if got := s.Len(); got != 7 {
		t.Errorf("transcript = %d messages, want the 7 the run left", got)
	}
	if got := transcript(s)[0]; got != "u1" {
		t.Errorf("first message = %q, want %q", got, "u1")
	}
}

// Compaction and KeepLast are the same job done two ways.
func TestCompactionSupersedesKeepLast(t *testing.T) {
	st := &stubSummariser{text: "S"}
	p := &recordingProvider{responses: []Response{{Message: AssistantText("done"), StopReason: StopEndTurn}}}
	a := &Agent{Provider: p, Model: "m", KeepLast: 2, Compaction: &CompactPolicy{
		CompactConfig: CompactConfig{KeepLast: 2, Summarise: st.fn},
		AtMessages:    5,
	}}

	s := seeded()
	if _, err := a.Run(context.Background(), s, "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	wantTranscript(t, s, SummaryPrefix+"S", "done")
}

func TestKeepLastStillTrimsWithoutCompaction(t *testing.T) {
	p := &recordingProvider{responses: []Response{{Message: AssistantText("done"), StopReason: StopEndTurn}}}
	a := &Agent{Provider: p, Model: "m", KeepLast: 2}

	s := seeded()
	res, err := a.Run(context.Background(), s, "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.CompactError != nil {
		t.Errorf("CompactError = %v, want nil with no Compaction set", res.CompactError)
	}
	wantTranscript(t, s, "go", "done")
	if s.Parent() != "" {
		t.Error("a trim archived nothing, so the session must have no parent")
	}
}

// The defer is on every exit.
func TestCompactionRunsOnAFailedRun(t *testing.T) {
	st := &stubSummariser{text: "S"}
	rs := toolCallThen("done")
	rs[0].Usage = Usage{InputTokens: 9, OutputTokens: 4}
	a := &Agent{
		Provider: &fakeProvider{responses: rs},
		Model:    "m", Tools: noopToolReg(t, nil), MaxSteps: 1,
		Compaction: &CompactPolicy{
			CompactConfig: CompactConfig{KeepLast: 2, Summarise: st.fn},
			AtMessages:    4,
		},
	}

	s := seeded()
	res, err := a.Run(context.Background(), s, "go")
	if !errors.Is(err, ErrMaxSteps) {
		t.Fatalf("Run err = %v, want ErrMaxSteps", err)
	}
	if st.calls != 1 {
		t.Fatalf("summariser called %d times, want 1", st.calls)
	}
	if got := transcript(s)[0]; !strings.HasPrefix(got, SummaryPrefix) {
		t.Errorf("first message = %q, want the summary", got)
	}

	if res.Steps != 1 {
		t.Errorf("Steps = %d, want 1", res.Steps)
	}
	if want := (Usage{InputTokens: 9, OutputTokens: 4}); res.Usage != want {
		t.Errorf("Result.Usage = %+v, want %+v", res.Usage, want)
	}
	if res.CompactError != nil {
		t.Errorf("CompactError = %v, want nil", res.CompactError)
	}
}

func TestCompactionTimeoutAbandonsAndKeepsTheTranscript(t *testing.T) {
	gate := make(chan struct{})
	defer close(gate)

	st := &stubSummariser{text: "S", gate: gate}
	p := &recordingProvider{responses: []Response{{Message: AssistantText("done"), StopReason: StopEndTurn}}}
	a := &Agent{Provider: p, Model: "m", Compaction: &CompactPolicy{
		CompactConfig: CompactConfig{KeepLast: 2, Summarise: st.fn},
		AtMessages:    5,
		Timeout:       20 * time.Millisecond,
	}}

	s := seeded()
	res, err := a.Run(context.Background(), s, "go")
	if err != nil {
		t.Fatalf("Run: %v; an abandoned compaction must not fail the run", err)
	}
	if st.calls != 1 {
		t.Fatalf("summariser called %d times, want 1", st.calls)
	}
	if !errors.Is(res.CompactError, context.DeadlineExceeded) {
		t.Errorf("CompactError = %v, want context.DeadlineExceeded", res.CompactError)
	}
	wantTranscript(t, s, "u1", "a1", "u2", "a2", "go", "done")
}
