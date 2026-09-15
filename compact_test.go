// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func compactTurn(q string) []Message {
	return []Message{
		UserText(q),
		{Role: RoleAssistant, Content: []Content{ToolUse{ID: "c-" + q, Name: "echo", Input: json.RawMessage(`{}`)}}},
		{Role: RoleTool, Content: []Content{ToolResult{ToolUseID: "c-" + q, Name: "echo", Content: ToolText("out")}}},
		AssistantText("answer " + q),
	}
}

func compactHistory(qs ...string) []Message {
	var h []Message
	for _, q := range qs {
		h = append(h, compactTurn(q)...)
	}
	return h
}

func compactSession(qs ...string) *Session {
	s := NewSession()
	s.Append(compactHistory(qs...)...)
	return s
}

func historyBytes(t *testing.T, s *Session) []byte {
	t.Helper()
	b, err := json.Marshal(s.History())
	if err != nil {
		t.Fatalf("marshal history: %v", err)
	}
	return b
}

func messagesJSON(t *testing.T, msgs []Message) string {
	t.Helper()
	b, err := json.Marshal(msgs)
	if err != nil {
		t.Fatalf("marshal messages: %v", err)
	}
	return string(b)
}

func fixedSummariser(text string) Summariser {
	return func(context.Context, []Message) (Summary, error) {
		return Summary{Text: text, Usage: Usage{InputTokens: 3, OutputTokens: 1}}, nil
	}
}

func wireUser(r Role) bool { return r == RoleUser || r == RoleTool }

func assertCompactSeam(t *testing.T, h []Message, k int, summarising bool) {
	t.Helper()
	kept := h[k:]
	if len(kept) == 0 {
		t.Fatalf("cut at %d left an empty tail", k)
	}
	if summarising {
		if wireUser(kept[0].Role) {
			t.Fatalf("cut at %d: tail starts with %q behind the summary's own user turn", k, kept[0].Role)
		}
	} else if kept[0].Role != RoleUser {
		t.Fatalf("cut at %d: tail starts with %q, want user-first", k, kept[0].Role)
	}

	calls := make(map[string]bool)
	for _, m := range kept {
		for _, c := range m.Content {
			if tu, ok := c.(ToolUse); ok {
				calls[tu.ID] = true
			}
		}
	}
	for _, m := range kept {
		for _, c := range m.Content {
			if tr, ok := c.(ToolResult); ok && !calls[tr.ToolUseID] {
				t.Fatalf("cut at %d separated tool_result %q from its call", k, tr.ToolUseID)
			}
		}
	}

	seam := kept
	if summarising {
		seam = append([]Message{UserText(SummaryPrefix + "s")}, kept...)
	}
	for i := 1; i < len(seam); i++ {
		if wireUser(seam[i-1].Role) && wireUser(seam[i].Role) {
			t.Fatalf("cut at %d put %q straight after %q", k, seam[i].Role, seam[i-1].Role)
		}
	}
}

func TestCompactSplitKeepsTurnsWhole(t *testing.T) {
	h := compactHistory("q1", "q2")
	for _, summarising := range []bool{false, true} {
		cuts := 0
		for keep := 1; keep <= len(h)+1; keep++ {
			k := compactSplit(h, keep, summarising)
			if k == 0 {
				continue
			}
			if k < 0 || k >= len(h) {
				t.Fatalf("compactSplit(keep=%d, summarising=%v) = %d, out of range for %d messages", keep, summarising, k, len(h))
			}
			cuts++
			assertCompactSeam(t, h, k, summarising)
		}
		if cuts == 0 {
			t.Fatalf("summarising=%v: no keepLast produced a cut, the case proves nothing", summarising)
		}
	}
}

func TestCompactSplitRefusesAnInvalidTail(t *testing.T) {
	call := Message{Role: RoleAssistant, Content: []Content{ToolUse{ID: "c1", Name: "echo", Input: json.RawMessage(`{}`)}}}
	result := Message{Role: RoleTool, Content: []Content{ToolResult{ToolUseID: "c1", Name: "echo", Content: ToolText("out")}}}
	cases := []struct {
		name string
		h    []Message
		keep int
	}{
		{"empty tail", []Message{UserText("a"), AssistantText("b"), UserText("c")}, 1},
		{"user-leading tail", []Message{UserText("a"), AssistantText("b"), UserText("c"), UserText("d")}, 2},
		{"tool-leading tail", []Message{UserText("a"), call, UserText("c"), result, AssistantText("e")}, 3},
		{"no user turn to align to", []Message{AssistantText("a"), AssistantText("b")}, 1},
		{"nothing to drop", compactHistory("q1"), 10},
		{"empty transcript", nil, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := compactSplit(tc.h, tc.keep, true); got != 0 {
				t.Fatalf("compactSplit = %d, want 0 (no-op) rather than an invalid transcript", got)
			}
		})
	}
}

func TestCompactArchivesUnderAFreshIDAndChains(t *testing.T) {
	s := compactSession("q1", "q2")
	liveID := s.ID()
	before := messagesJSON(t, s.History())

	res, err := s.Compact(context.Background(), CompactConfig{KeepLast: 4, Summarise: fixedSummariser("first")})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	first := res.Archive
	if first == nil {
		t.Fatal("Compact returned no archive")
	}
	if first.ID() == liveID {
		t.Fatal("the archive reused the live id; an external reference to the conversation would now point at the archive")
	}
	if got := messagesJSON(t, first.History()); got != before {
		t.Fatalf("archive history = %s, want the full pre-compaction transcript %s", got, before)
	}
	if s.ID() != liveID {
		t.Fatalf("live session id changed to %q, want %q", s.ID(), liveID)
	}
	if s.Parent() != first.ID() {
		t.Fatalf("live Parent = %q, want the archive %q", s.Parent(), first.ID())
	}
	if first.Parent() != "" {
		t.Fatalf("archive Parent = %q, want the live session's previous parent (empty)", first.Parent())
	}
	if res.Dropped != 5 {
		t.Fatalf("Dropped = %d, want 5", res.Dropped)
	}
	if res.Usage != (Usage{InputTokens: 3, OutputTokens: 1}) {
		t.Fatalf("Usage = %+v, want the summariser's own cost", res.Usage)
	}
	h := s.History()
	if len(h) != 4 {
		t.Fatalf("live history = %d messages, want 4 (summary + the kept turn)", len(h))
	}
	if h[0].Role != RoleUser || h[0].Text() != SummaryPrefix+"first" {
		t.Fatalf("live history[0] = %q %q, want a user turn carrying the prefixed summary", h[0].Role, h[0].Text())
	}

	s.Append(compactTurn("q3")...)
	second, err := s.Compact(context.Background(), CompactConfig{KeepLast: 4, Summarise: fixedSummariser("second")})
	if err != nil {
		t.Fatalf("second Compact: %v", err)
	}
	if second.Archive.Parent() != first.ID() {
		t.Fatalf("second archive Parent = %q, want the first archive %q; the chain forked", second.Archive.Parent(), first.ID())
	}
	if s.Parent() != second.Archive.ID() {
		t.Fatalf("live Parent = %q, want the second archive %q", s.Parent(), second.Archive.ID())
	}
	if s.ID() != liveID {
		t.Fatalf("live session id changed to %q across a second compaction", s.ID())
	}
}

func TestCompactCommitsOnlyOnceTheArchiveIsAway(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name string
		cfg  func(*testing.T) CompactConfig
	}{
		{"summariser fails", func(t *testing.T) CompactConfig {
			return CompactConfig{
				KeepLast:  4,
				Summarise: func(context.Context, []Message) (Summary, error) { return Summary{}, boom },
				Archive: func(context.Context, *Session) error {
					t.Error("Archive ran after the summariser failed")
					return nil
				},
			}
		}},
		{"archive fails", func(*testing.T) CompactConfig {
			return CompactConfig{
				KeepLast:  4,
				Summarise: fixedSummariser("sum"),
				Archive:   func(context.Context, *Session) error { return boom },
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := compactSession("q1", "q2")
			before := historyBytes(t, s)
			rev := sessionRev(s)

			res, err := s.Compact(context.Background(), tc.cfg(t))
			if !errors.Is(err, boom) {
				t.Fatalf("Compact err = %v, want %v", err, boom)
			}
			if res.Archive != nil || res.Dropped != 0 {
				t.Fatalf("failed compaction reported %+v, want a zero result", res)
			}
			if after := historyBytes(t, s); !bytes.Equal(before, after) {
				t.Fatalf("transcript changed despite the failure:\n before %s\n after  %s", before, after)
			}
			if s.Parent() != "" {
				t.Fatalf("Parent = %q after a failed compaction, want it unchanged", s.Parent())
			}
			if got := sessionRev(s); got != rev {
				t.Fatalf("rev = %d, want %d unchanged", got, rev)
			}
		})
	}
}

func TestCompactWithoutASummariserIsAnArchivingTrim(t *testing.T) {
	s := compactSession("q1", "q2")
	before := messagesJSON(t, s.History())

	res, err := s.Compact(context.Background(), CompactConfig{KeepLast: 4})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if res.Archive == nil {
		t.Fatal("a nil summariser still archives")
	}
	if got := messagesJSON(t, res.Archive.History()); got != before {
		t.Fatalf("archive history = %s, want the full transcript", got)
	}
	if res.Usage != (Usage{}) {
		t.Fatalf("Usage = %+v, want zero with no summariser call", res.Usage)
	}
	if res.Dropped != 4 {
		t.Fatalf("Dropped = %d, want 4 (Trim's own boundary)", res.Dropped)
	}
	h := s.History()
	if len(h) != 4 {
		t.Fatalf("live history = %d messages, want the 4 kept verbatim", len(h))
	}
	if h[0].Role != RoleUser || h[0].Text() != "q2" {
		t.Fatalf("live history[0] = %q %q, want the original user turn with nothing inserted", h[0].Role, h[0].Text())
	}
	if strings.Contains(messagesJSON(t, h), SummaryPrefix) {
		t.Fatal("a summary was inserted without a summariser")
	}
}

func TestCompactKeepsMessagesAppendedDuringTheSummary(t *testing.T) {
	s := compactSession("q1", "q2")
	entered := make(chan []Message, 1)
	release := make(chan struct{})
	cfg := CompactConfig{KeepLast: 4, Summarise: func(_ context.Context, msgs []Message) (Summary, error) {
		entered <- msgs
		<-release
		return Summary{Text: "sum", Usage: Usage{InputTokens: 7}}, nil
	}}

	type outcome struct {
		res CompactResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := s.Compact(context.Background(), cfg)
		done <- outcome{res, err}
	}()

	head := <-entered
	s.Append(UserText("late"))
	close(release)
	got := <-done

	if got.err != nil {
		t.Fatalf("Compact: %v", got.err)
	}
	if len(head) != 5 {
		t.Fatalf("summariser saw %d messages, want the 5 it is replacing", len(head))
	}
	if strings.Contains(messagesJSON(t, head), "late") {
		t.Fatal("a message appended during the summariser was summarised; it is tail, not head")
	}
	h := s.History()
	if len(h) != 5 {
		t.Fatalf("live history = %d messages, want 5 (summary + 3 kept + the appended turn)", len(h))
	}
	if h[0].Text() != SummaryPrefix+"sum" {
		t.Fatalf("history[0] = %q, want the summary", h[0].Text())
	}
	if last := h[len(h)-1]; last.Role != RoleUser || last.Text() != "late" {
		t.Fatalf("history tail = %q %q, want the message appended during the summary", last.Role, last.Text())
	}
	if got.res.Dropped != 5 {
		t.Fatalf("Dropped = %d, want 5", got.res.Dropped)
	}
	if got.res.Usage != (Usage{InputTokens: 7}) {
		t.Fatalf("Usage = %+v, want the summariser's", got.res.Usage)
	}
}

func TestCompactAbandonsWhenTheTranscriptIsRewrittenUnderIt(t *testing.T) {
	s := compactSession("q1", "q2")
	entered := make(chan struct{})
	release := make(chan struct{})
	cfg := CompactConfig{KeepLast: 4, Summarise: func(context.Context, []Message) (Summary, error) {
		close(entered)
		<-release
		return Summary{Text: "sum"}, nil
	}}

	type outcome struct {
		res CompactResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := s.Compact(context.Background(), cfg)
		done <- outcome{res, err}
	}()

	<-entered
	s.Trim(4)
	trimmed := historyBytes(t, s)
	close(release)
	got := <-done

	if !errors.Is(got.err, ErrSessionChanged) {
		t.Fatalf("Compact err = %v, want ErrSessionChanged", got.err)
	}
	if got.res.Archive != nil || got.res.Dropped != 0 {
		t.Fatalf("abandoned compaction reported %+v, want a zero result", got.res)
	}
	if after := historyBytes(t, s); !bytes.Equal(trimmed, after) {
		t.Fatalf("session moved after the Trim:\n trim  %s\n after %s", trimmed, after)
	}
	h := s.History()
	if len(h) != 4 || h[0].Role != RoleUser || h[0].Text() != "q2" {
		t.Fatalf("history = %d messages starting %q %q, want the Trim's own result", len(h), h[0].Role, h[0].Text())
	}
	if s.Parent() != "" {
		t.Fatalf("Parent = %q, want it unset by an abandoned compaction", s.Parent())
	}
}

func TestCompactWithNothingToDropIsANoOp(t *testing.T) {
	s := NewSession()
	s.Append(UserText("q"), AssistantText("a"))
	before := historyBytes(t, s)
	rev := sessionRev(s)

	res, err := s.Compact(context.Background(), CompactConfig{
		KeepLast: 10,
		Summarise: func(context.Context, []Message) (Summary, error) {
			t.Error("summariser called with nothing to drop")
			return Summary{}, nil
		},
		Archive: func(context.Context, *Session) error {
			t.Error("Archive called with nothing to drop")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if res.Archive != nil {
		t.Fatalf("Archive = %v, want nil when there was nothing to compact", res.Archive.ID())
	}
	if res.Dropped != 0 || res.Usage != (Usage{}) {
		t.Fatalf("result = %+v, want zero", res)
	}
	if after := historyBytes(t, s); !bytes.Equal(before, after) {
		t.Fatalf("history changed: %s", after)
	}
	if s.Parent() != "" {
		t.Fatalf("Parent = %q, want empty", s.Parent())
	}
	if got := sessionRev(s); got != rev {
		t.Fatalf("rev = %d, want %d unchanged", got, rev)
	}
}

func TestCompactArchiveCarriesStateAndUsage(t *testing.T) {
	s := compactSession("q1", "q2")
	if err := s.SetState("cursor", 42); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	billed := Usage{InputTokens: 100, OutputTokens: 20}
	s.addUsage(billed)

	res, err := s.Compact(context.Background(), CompactConfig{KeepLast: 4, Summarise: fixedSummariser("sum")})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	arc := res.Archive
	if got := arc.Usage(); got != billed {
		t.Fatalf("archive Usage = %+v, want %+v", got, billed)
	}
	if got := s.Usage(); got != billed {
		t.Fatalf("live Usage = %+v, want %+v; compacting is not a refund", got, billed)
	}
	v, ok, err := GetState[int](arc, "cursor")
	if err != nil || !ok || v != 42 {
		t.Fatalf("archive state cursor = %d, %v, %v; want 42 carried across", v, ok, err)
	}
	if err := s.SetState("cursor", 99); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	if v, _, _ := GetState[int](arc, "cursor"); v != 42 {
		t.Fatalf("archive state cursor = %d after a live write; the archive aliases the session", v)
	}
}

type compactStubProvider struct {
	mu   sync.Mutex
	req  Request
	resp Response
	err  error
}

func (p *compactStubProvider) Name() string               { return "compact-stub" }
func (p *compactStubProvider) Capabilities() Capabilities { return Capabilities{} }

func (p *compactStubProvider) Complete(_ context.Context, req Request) (Response, error) {
	p.mu.Lock()
	p.req = req
	p.mu.Unlock()
	return p.resp, p.err
}

func (p *compactStubProvider) Stream(ctx context.Context, req Request, fn func(StreamEvent) error) (Response, error) {
	resp, err := p.Complete(ctx, req)
	if err != nil {
		return Response{}, err
	}
	if err := fn(StreamEvent{Type: EventDone}); err != nil {
		return Response{}, err
	}
	return resp, nil
}

func (p *compactStubProvider) request() Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.req
}

func TestSummariseWithSendsOneRenderedUserTurn(t *testing.T) {
	p := &compactStubProvider{resp: Response{
		Message:    AssistantText("the summary"),
		StopReason: StopEndTurn,
		Usage:      Usage{InputTokens: 11, OutputTokens: 3},
	}}
	msgs := compactHistory("q1")

	sum, err := SummariseWith(p, "m", "")(context.Background(), msgs)
	if err != nil {
		t.Fatalf("summarise: %v", err)
	}
	if sum.Text != "the summary" {
		t.Fatalf("Text = %q, want the model's reply", sum.Text)
	}
	if sum.Usage != (Usage{InputTokens: 11, OutputTokens: 3}) {
		t.Fatalf("Usage = %+v, want the call's own cost", sum.Usage)
	}

	req := p.request()
	if req.Model != "m" {
		t.Errorf("Model = %q, want m", req.Model)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != RoleUser {
		t.Fatalf("Messages = %+v, want one user turn", req.Messages)
	}
	if got := req.Messages[0].Text(); got != renderTranscript(msgs) {
		t.Fatalf("user turn = %q, want the rendered transcript %q", got, renderTranscript(msgs))
	}
	if len(req.Messages[0].Content) != 1 {
		t.Fatalf("user turn carries %d blocks, want the rendered text alone", len(req.Messages[0].Content))
	}
	if len(req.Tools) != 0 {
		t.Errorf("Tools = %+v, want none on a summary call", req.Tools)
	}
	if len(req.System) != 1 || req.System[0].Text != DefaultSummaryPrompt {
		t.Fatalf("System = %+v, want the default prompt as the system prompt", req.System)
	}

	custom := &compactStubProvider{resp: Response{Message: AssistantText("s"), StopReason: StopEndTurn}}
	if _, err := SummariseWith(custom, "m", "just the facts")(context.Background(), msgs); err != nil {
		t.Fatalf("summarise: %v", err)
	}
	if sys := custom.request().System; len(sys) != 1 || sys[0].Text != "just the facts" {
		t.Fatalf("System = %+v, want the custom prompt in place of the default", sys)
	}
}

func TestSummariseWithReturnsTheUsageItsFailuresCost(t *testing.T) {
	spent := Usage{InputTokens: 9, OutputTokens: 1}
	boom := errors.New("provider down")

	failing := &compactStubProvider{resp: Response{Usage: spent}, err: boom}
	sum, err := SummariseWith(failing, "m", "")(context.Background(), compactHistory("q1"))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if sum.Usage != spent {
		t.Fatalf("Usage = %+v, want %+v; a failed summary still cost tokens", sum.Usage, spent)
	}
	if sum.Text != "" {
		t.Fatalf("Text = %q, want empty on failure", sum.Text)
	}

	refusing := &compactStubProvider{resp: Response{
		Message: AssistantText("I will not"), StopReason: StopRefusal, Usage: spent,
	}}
	sum, err = SummariseWith(refusing, "m", "")(context.Background(), compactHistory("q1"))
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v, want a refusal error", err)
	}
	if sum.Usage != spent {
		t.Fatalf("Usage = %+v, want %+v; a refusal still cost tokens", sum.Usage, spent)
	}
	if sum.Text != "" {
		t.Fatalf("Text = %q, want empty on a refusal", sum.Text)
	}
}

func TestRenderTranscriptNamesWhatItCannotWrite(t *testing.T) {
	msgs := []Message{
		UserText("hello"),
		{Role: RoleAssistant, Content: []Content{
			Thinking{Text: "secret reasoning", Signature: "sig"},
			Text{Text: "thinking done"},
			Plan{Steps: []string{"a", "b"}},
		}},
		{Role: RoleUser, Content: []Content{
			Image{MediaType: "image/png", Data: []byte{1, 2}},
			Audio{MediaType: "audio/wav", Data: []byte{3}},
		}},
		{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "c1", Name: "weather", Input: json.RawMessage(`{"city":"Leeds"}`)},
		}},
		{Role: RoleTool, Content: []Content{
			ToolResult{ToolUseID: "c1", Name: "weather", Content: ToolText("sunny")},
		}},
		{Role: RoleTool, Content: []Content{
			ToolResult{ToolUseID: "c2", Name: "weather", Content: ToolText("no such city"), IsError: true},
		}},
		{Role: RoleAssistant, Content: []Content{Thinking{Text: "private"}}},
	}

	got := renderTranscript(msgs)
	for _, want := range []string{
		"user: hello\n",
		"assistant: thinking done[plan: a; b]\n",
		"user: [image image/png][audio audio/wav]\n",
		"assistant: [calls weather {\"city\":\"Leeds\"}]\n",
		"tool: [weather returned: sunny]\n",
		"tool: [weather returned (error): no such city]\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("transcript missing %q:\n%s", want, got)
		}
	}
	for _, hidden := range []string{"secret reasoning", "private", "sig"} {
		if strings.Contains(got, hidden) {
			t.Errorf("transcript leaked the model's reasoning %q:\n%s", hidden, got)
		}
	}
	if n := strings.Count(got, "\n"); n != 6 {
		t.Errorf("transcript has %d lines, want 6 (the thinking-only turn writes none):\n%s", n, got)
	}
}

// What a compaction actually leaves, spelled out.
func TestCompactLeavesSummaryThenAnAssistantTail(t *testing.T) {
	s := NewSession()
	for i := 1; i <= 3; i++ {
		s.Append(UserText(fmt.Sprintf("u%d", i)), AssistantText(fmt.Sprintf("a%d", i)))
	}
	res, err := s.Compact(context.Background(), CompactConfig{
		KeepLast: 2,
		Summarise: func(context.Context, []Message) (Summary, error) {
			return Summary{Text: "SUMMARY"}, nil
		},
	})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if res.Archive == nil {
		t.Fatal("no archive")
	}
	h := s.History()
	if len(h) != 2 {
		t.Fatalf("%d messages kept, want 2 (the summary plus the tail after the folded turn)", len(h))
	}
	if h[0].Role != RoleUser || !strings.Contains(h[0].Text(), "SUMMARY") {
		t.Errorf("first message = %s %q, want the summary as a user turn", h[0].Role, h[0].Text())
	}
	if h[1].Role != RoleAssistant || h[1].Text() != "a3" {
		t.Errorf("second message = %s %q, want assistant a3", h[1].Role, h[1].Text())
	}

	for i := 1; i < len(h); i++ {
		if h[i].Role == RoleUser && h[i-1].Role == RoleUser {
			t.Errorf("two consecutive user messages at %d", i)
		}
	}

	if got := len(res.Archive.History()); got != 6 {
		t.Errorf("archive holds %d messages, want the original 6", got)
	}
}
