// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"unicode/utf8"
)

const defaultMaxSteps = 8

const defaultMaxParallelTools = 8

// Neutral, framework-side errors a caller can branch on with errors.Is.
var (
	// ErrMaxSteps wraps the error returned.
	ErrMaxSteps = errors.New("golm: max steps reached")

	// ErrNoProvider is returned by Run/Stream when the Agent has no Provider set.
	ErrNoProvider = errors.New("golm: agent has no Provider")

	// ErrRefused is the model declining.
	ErrRefused = errors.New("golm: model refused")

	// ErrContextOverflow is the CONTEXT window exhausted.
	ErrContextOverflow = errors.New("golm: context window exhausted")

	// ErrSessionBusy is a second concurrent Run/Stream on the SAME Session.
	ErrSessionBusy = errors.New("golm: session already has a run in flight")

	// ErrNoSession is Run/Stream with a nil Session.
	ErrNoSession = errors.New("golm: run requires a Session")

	// ErrTokenBudget is MaxTotalTokens reached.
	ErrTokenBudget = errors.New("golm: token budget exhausted")
)

// StopError is a terminal stop the loop could not turn into an answer.
type StopError struct {
	StopReason StopReason
	Response   Response
	err        error
}

func (e *StopError) Error() string {
	return fmt.Sprintf("%v (stop reason %q)", e.err, e.StopReason)
}

func (e *StopError) Unwrap() error { return e.err }

func stopError(resp Response) error {
	var sentinel error
	switch resp.StopReason {
	case StopRefusal:
		sentinel = ErrRefused
	case StopContextOverflow:
		sentinel = ErrContextOverflow
	default:
		return nil
	}
	return &StopError{StopReason: resp.StopReason, Response: resp, err: sentinel}
}

// Agent is the configuration a conversation is run with.
type Agent struct {
	Provider Provider
	Model    string
	System   string

	SystemPrompt SystemPrompt
	Tools        *Registry
	MaxTokens    int

	Temperature *float64
	Thinking    ThinkingConfig

	Effort Effort

	Safety SafetyLevel

	ToolChoice string

	ResponseModalities []string
	MaxSteps           int

	MaxTotalTokens int

	Budget *Budget

	Compaction *CompactPolicy

	KeepLast int

	CachePrompt bool

	CachePromptTTL CacheTTL

	Name          string
	Bus           *Bus
	ParallelTools bool

	MaxParallelTools int

	ToolPolicy ToolPolicy

	ToolTimeout time.Duration

	MaxToolResultBytes int

	Logger *slog.Logger

	OnToolResult func(ToolResult)
}

// Result is what one Run/Stream produced.
type Result struct {
	Message Message

	StopReason StopReason

	Response Response

	Steps int

	ToolErrors int

	ToolDenials int

	Usage Usage

	StepUsage []Usage

	CompactError error
}

// Text is the final message's text.
func (r Result) Text() string { return r.Message.Text() }

// NewAgent returns an Agent ready to run p with the given model.
func NewAgent(p Provider, model string) *Agent {
	return &Agent{Provider: p, Model: model}
}

// Validate reports why the agent cannot run.
func (a *Agent) Validate() error {
	if a.Provider == nil {
		return ErrNoProvider
	}
	if a.Model == "" {
		return errors.New("golm: agent has no Model")
	}
	return nil
}

func (a *Agent) maxSteps() int {
	if a.MaxSteps > 0 {
		return a.MaxSteps
	}
	return defaultMaxSteps
}

func (a *Agent) emit(ctx context.Context, kind string, data any) {
	if a.Bus != nil {
		a.Bus.Publish(ctx, Event{Topic: a.Name, Agent: a.Name, Kind: kind, Data: data})
	}
	a.log(ctx, kind, data)
}

func (a *Agent) systemPrompt() SystemPrompt {
	p := a.SystemPrompt
	if a.System != "" {
		p = append(SystemPrompt{{Text: a.System, Cache: len(p.Sections()) == 0}}, p...)
	}
	if !a.CachePrompt {
		return p.uncached()
	}
	return p
}

func (a *Agent) request(s *Session) Request {
	var defs []ToolDef
	if a.Tools != nil {
		defs = a.Tools.Defs()
	}
	msgs := s.view()
	var cache CacheConfig
	if a.CachePrompt {
		cache = CacheConfig{MessagePrefix: len(msgs), TTL: a.CachePromptTTL}
	}
	return Request{
		Model:              a.Model,
		System:             a.systemPrompt(),
		Messages:           msgs,
		Tools:              defs,
		ToolChoice:         a.ToolChoice,
		MaxTokens:          a.MaxTokens,
		Temperature:        a.Temperature,
		Thinking:           a.Thinking,
		Effort:             a.Effort,
		Safety:             a.Safety,
		ResponseModalities: a.ResponseModalities,
		Cache:              cache,
	}
}

func (a *Agent) execTool(ctx context.Context, u ToolUse) (res ToolResult) {
	defer func() {
		res.Name = u.Name
		res.Content = a.boundToolResult(res.Content)
	}()
	if a.Tools == nil {
		return ToolResult{ToolUseID: u.ID, Content: ToolText(fmt.Sprintf("no tools registered (requested %q)", u.Name)), IsError: true}
	}
	t, ok := a.Tools.Get(u.Name)
	if !ok {
		return ToolResult{ToolUseID: u.ID, Content: ToolText(fmt.Sprintf("unknown tool %q", u.Name)), IsError: true}
	}
	if a.ToolTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, a.ToolTimeout)
		defer cancel()
	}

	defer func() {
		if r := recover(); r != nil {
			res = ToolResult{ToolUseID: u.ID, Content: ToolText(fmt.Sprintf("tool panicked: %v", r)), IsError: true}
		}
	}()

	out, err := t.Execute(WithToolCall(ctx, u.ID), u.Input)
	if err != nil {
		return ToolResult{ToolUseID: u.ID, Content: ToolText(err.Error()), IsError: true}
	}
	return ToolResult{ToolUseID: u.ID, Content: out}
}

func (a *Agent) decide(ctx context.Context, step int, u ToolUse) (c context.Context, err error) {
	if a.ToolPolicy == nil {
		return ctx, nil
	}
	var t Tool
	if a.Tools != nil {
		t, _ = a.Tools.Get(u.Name)
	}
	defer func() {
		if r := recover(); r != nil {
			c, err = nil, fmt.Errorf("%w: policy panicked: %v", ErrToolDenied, r)
		}
	}()
	c, err = a.ToolPolicy(ctx, ToolRequest{Agent: a.Name, Step: step, Tool: t, Call: u})
	if err != nil {
		return nil, err
	}
	if c == nil {
		c = ctx
	}
	return c, nil
}

const toolAbandonGrace = 250 * time.Millisecond

func (a *Agent) toolWaitCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if a.ToolTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, a.ToolTimeout+toolAbandonGrace)
}

func (a *Agent) boundToolResult(out []ToolContent) []ToolContent {
	if a.MaxToolResultBytes <= 0 {
		return out
	}
	left := a.MaxToolResultBytes
	bounded := make([]ToolContent, 0, len(out))
	for _, c := range out {
		if t, ok := c.(Text); ok {
			if len(t.Text) <= left {
				left -= len(t.Text)
				bounded = append(bounded, t)
				continue
			}
			kept := truncRunes(t.Text, left)
			bounded = append(bounded, Text{Text: kept +
				fmt.Sprintf("\n… (truncated, %d bytes omitted)", len(t.Text)-len(kept))})
			left = 0
			continue
		}
		n := mediaBytes(c)
		if n <= left {
			left -= n
			bounded = append(bounded, c)
			continue
		}
		bounded = append(bounded, Text{Text: fmt.Sprintf("… (%s omitted, %d bytes over the result cap)", c.Kind(), n)})
	}
	return bounded
}

func truncRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func mediaBytes(c ToolContent) int {
	switch v := c.(type) {
	case Image:
		return len(v.Data) + len(v.URL)
	case Audio:
		return len(v.Data)
	}
	return 0
}

func (a *Agent) observe(ctx context.Context, resp Response, step int) (msg Message, toolErrs, denials int, cont bool) {
	uses := resp.Message.ToolUses()
	if len(uses) == 0 || !executableStop(resp.StopReason) {
		return Message{}, 0, 0, false
	}
	results := make([]Content, len(uses))

	var toolCtx []context.Context

	type slot struct {
		i   int
		res ToolResult
	}
	done := make(chan slot, len(uses))
	runOne := func(i int, u ToolUse) {
		tctx := ctx
		if toolCtx != nil && toolCtx[i] != nil {
			tctx = toolCtx[i]
		}
		a.emit(ctx, "tool", ToolCallEvent{Name: u.Name, ID: u.ID, Input: u.Input})
		res := a.execTool(tctx, u)
		a.emit(ctx, "tool_result", ToolResultEvent{Name: res.Name, ID: res.ToolUseID, IsError: res.IsError, Content: res.Text()})
		a.streamToolResult(ctx, res)
		done <- slot{i, res}
	}
	defer func() {
		for _, c := range results {
			tr, ok := c.(ToolResult)
			if !ok {
				continue
			}
			if tr.IsError {
				toolErrs++
			}
			a.report(ctx, tr)
		}
	}()

	started := make([]bool, len(uses))

	if a.ToolPolicy != nil {
		toolCtx = make([]context.Context, len(uses))
		for i, u := range uses {
			tctx, err := a.decide(ctx, step, u)
			if err != nil {
				denials++
				res := ToolResult{
					ToolUseID: u.ID,
					Name:      u.Name,
					Content:   ToolText(fmt.Sprintf("tool %q was not run: %v", u.Name, err)),
					IsError:   true,
				}
				results[i] = res
				a.emit(ctx, "tool", ToolCallEvent{Name: u.Name, ID: u.ID, Input: u.Input})
				a.emit(ctx, "tool_result", ToolResultEvent{Name: res.Name, ID: res.ToolUseID, IsError: true, Content: res.Text()})

				a.streamToolResult(ctx, res)
				continue
			}
			toolCtx[i] = tctx
		}
	}
	if a.ParallelTools && len(uses) > 1 {
		limit := a.MaxParallelTools
		if limit <= 0 {
			limit = defaultMaxParallelTools
		}
		sem := make(chan struct{}, limit)

		wctx, cancel := a.toolWaitCtx(ctx)
		for i, u := range uses {
			if results[i] != nil {
				continue
			}
			select {
			case sem <- struct{}{}:
			case <-wctx.Done():
			}
			if wctx.Err() != nil {
				break
			}
			started[i] = true
			go func(i int, u ToolUse) {
				defer func() { <-sem }()
				runOne(i, u)
			}(i, u)
		}

		for n := 0; n < countTrue(started); n++ {
			select {
			case s := <-done:
				results[s.i] = s.res
			case <-wctx.Done():
				n = len(uses)
			}
		}
		cancel()
	} else {
		for i, u := range uses {
			if results[i] != nil {
				continue
			}
			if ctx.Err() != nil {
				break
			}
			started[i] = true
			go runOne(i, u)
			wctx, cancel := a.toolWaitCtx(ctx)
			select {
			case s := <-done:
				results[s.i] = s.res
				cancel()
				continue
			case <-wctx.Done():
			}
			cancel()

			break
		}
	}

	for i, u := range uses {
		if results[i] != nil {
			continue
		}
		reason := "was not executed: an earlier tool call in this turn did not return before the deadline"
		if started[i] {
			reason = "did not return before the deadline"
		}
		results[i] = ToolResult{
			ToolUseID: u.ID,
			Name:      u.Name,
			Content:   ToolText(fmt.Sprintf("tool %q %s", u.Name, reason)),
			IsError:   true,
		}
	}
	return Message{Role: RoleTool, Content: results}, toolErrs, denials, true
}

func (a *Agent) streamToolResult(ctx context.Context, res ToolResult) {
	out := streamSinkOf(ctx)
	if out == nil {
		return
	}
	_ = out.emit(StreamEvent{
		Type: EventToolResult, ToolID: res.ToolUseID, ToolName: res.Name,
		Text: res.Text(), ToolError: res.IsError,
		Agent: a.Name, Depth: delegationDepth(ctx),
	})
}

func (a *Agent) report(ctx context.Context, tr ToolResult) {
	if a.OnToolResult == nil {
		return
	}
	defer func() {
		if p := recover(); p != nil {
			a.emit(ctx, "error", fmt.Sprintf("OnToolResult panicked: %v", p))
		}
	}()
	a.OnToolResult(tr)
}

func countTrue(b []bool) int {
	n := 0
	for _, v := range b {
		if v {
			n++
		}
	}
	return n
}

func (a *Agent) begin(ctx context.Context, s *Session, msg Message) error {
	if a.Provider == nil {
		return ErrNoProvider
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.acquire() {
		return ErrSessionBusy
	}
	a.emit(ctx, "start", msg.Text())
	s.Append(msg)
	return nil
}

// Run appends input as a user message to s and drives the ReAct loop to completion.
func (a *Agent) Run(ctx context.Context, s *Session, input string) (Result, error) {
	return a.RunMessage(ctx, s, UserText(input))
}

// RunMessage is like Run but takes a full Message, allowing multimodal input.
func (a *Agent) RunMessage(ctx context.Context, s *Session, msg Message) (Result, error) {
	if s == nil {
		return Result{}, ErrNoSession
	}
	base := s.Len()
	if err := a.begin(ctx, s, msg); err != nil {
		return Result{}, err
	}
	defer s.release()
	return a.drive(ctx, s, base, func(ctx context.Context) (Response, error) {
		return a.Provider.Complete(ctx, a.request(s))
	})
}

func executableStop(r StopReason) bool { return r == StopToolUse || r == StopEndTurn }

func forcedToolAnswer(resp Response) Message {
	uses := resp.Message.ToolUses()
	results := make([]Content, len(uses))
	for i, u := range uses {
		results[i] = ToolResult{ToolUseID: u.ID, Name: u.Name, Content: ToolText("structured-output tool call recorded; not executed")}
	}
	return Message{Role: RoleTool, Content: results}
}

func (a *Agent) answerTruncatedTools(resp Response) (Message, bool) {
	if executableStop(resp.StopReason) {
		return Message{}, false
	}
	uses := resp.Message.ToolUses()
	if len(uses) == 0 {
		return Message{}, false
	}
	results := make([]Content, len(uses))
	for i, u := range uses {
		results[i] = ToolResult{ToolUseID: u.ID, Name: u.Name, Content: ToolText(fmt.Sprintf("tool call not executed: response stopped with reason %q, input may be incomplete", resp.StopReason)), IsError: true}
	}
	return Message{Role: RoleTool, Content: results}, true
}

func (a *Agent) drive(ctx context.Context, s *Session, base int, step func(context.Context) (Response, error)) (res Result, err error) {
	defer a.finish(ctx, s, &res)

	defer func() {
		if err != nil {
			s.rollback(base)
		}
	}()
	for i := 0; i < a.maxSteps(); i++ {
		if err := ctx.Err(); err != nil {
			a.emit(ctx, "error", err.Error())
			return res, err
		}

		if spent := res.Usage.Total(); a.MaxTotalTokens > 0 && spent >= a.MaxTotalTokens {
			a.emit(ctx, "error", "token budget exhausted")
			return res, fmt.Errorf("%w (%d/%d)", ErrTokenBudget, spent, a.MaxTotalTokens)
		}
		if a.Budget.Exhausted() {
			a.emit(ctx, "error", "shared token budget exhausted")
			return res, a.Budget.err()
		}
		resp, err := step(ctx)

		res.Steps = i + 1
		if err != nil {
			a.book(s, &res, resp.Usage)
			a.emit(ctx, "error", err.Error())
			return res, err
		}
		a.book(s, &res, resp.Usage)
		res.StopReason = resp.StopReason
		res.Response = resp
		a.emit(ctx, "step", StepEvent{Step: i + 1, StopReason: resp.StopReason, Usage: resp.Usage})
		s.Append(resp.Message)
		res.Message = resp.Message
		a.emit(ctx, "message", resp.Message.Text())

		if a.ToolChoice != "" && executableStop(resp.StopReason) && len(resp.Message.ToolUses()) > 0 {
			s.Append(forcedToolAnswer(resp))
			a.emit(ctx, "done", res.Message.Text())
			return res, nil
		}
		if toolMsg, toolErrs, denials, cont := a.observe(ctx, resp, i+1); cont {
			res.ToolErrors += toolErrs
			res.ToolDenials += denials
			s.Append(toolMsg)
			continue
		}
		switch resp.StopReason {
		case StopPause:

			a.emit(ctx, "paused", res.Message.Text())
			if recovered, ok := a.answerTruncatedTools(resp); ok {
				s.Append(recovered)
			}
			continue
		case StopContextOverflow, StopRefusal:

			if recovered, ok := a.answerTruncatedTools(resp); ok {
				s.Append(recovered)
			}
			a.emit(ctx, "done", res.Message.Text())
			return res, stopError(resp)
		case StopOther:

			if recovered, ok := a.answerTruncatedTools(resp); ok {
				s.Append(recovered)
			}
			a.emit(ctx, "done", res.Message.Text())
			return res, nil
		default:

			if recovered, ok := a.answerTruncatedTools(resp); ok {
				s.Append(recovered)
				continue
			}
			a.emit(ctx, "done", res.Message.Text())
			return res, nil
		}
	}
	a.emit(ctx, "error", "max steps reached")
	return res, fmt.Errorf("%w (%d)", ErrMaxSteps, a.maxSteps())
}

func (a *Agent) finish(ctx context.Context, s *Session, res *Result) {
	if a.Compaction == nil {
		if a.KeepLast > 0 {
			s.Trim(a.KeepLast)
		}
		return
	}
	if !a.compactionDue(s, res) {
		return
	}

	if err := ctx.Err(); err != nil {
		res.CompactError = err
		return
	}
	timeout := a.Compaction.Timeout
	if timeout <= 0 {
		timeout = defaultCompactTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := s.Compact(cctx, a.Compaction.CompactConfig)
	if err != nil {
		res.CompactError = err
		a.emit(ctx, "error", "compaction: "+err.Error())
		return
	}

	s.addUsage(out.Usage)
	a.Budget.Spend(out.Usage)
}

func (a *Agent) compactionDue(s *Session, res *Result) bool {
	p := a.Compaction
	if p.AtMessages > 0 && s.Len() > p.AtMessages {
		return true
	}
	return p.AtInputTokens > 0 && res.Response.Usage.InputTokens >= p.AtInputTokens
}

func (a *Agent) book(s *Session, res *Result, u Usage) {
	res.Usage.Add(u)
	res.StepUsage = append(res.StepUsage, u)
	s.addUsage(u)

	a.Budget.Spend(u)
}

// Stream is like Run but streams incremental events through fn as they arrive.
func (a *Agent) Stream(ctx context.Context, s *Session, input string, fn func(StreamEvent) error) (Result, error) {
	return a.StreamMessage(ctx, s, UserText(input), fn)
}

// StreamMessage is like Stream but takes a full.
func (a *Agent) StreamMessage(ctx context.Context, s *Session, msg Message, fn func(StreamEvent) error) (Result, error) {
	if s == nil {
		return Result{}, ErrNoSession
	}
	base := s.Len()
	if err := a.begin(ctx, s, msg); err != nil {
		return Result{}, err
	}
	defer s.release()

	out := &sink{fn: fn, depth: delegationDepth(ctx)}
	ctx = withStreamSink(ctx, out)
	return a.drive(ctx, s, base, func(ctx context.Context) (Response, error) {
		return a.Provider.Stream(ctx, a.request(s), func(ev StreamEvent) error {
			if ev.Agent == "" {
				ev.Agent = a.Name
			}
			if ev.Depth == 0 {
				ev.Depth = out.depth
			}
			return out.emit(ev)
		})
	})
}
