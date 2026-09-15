// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// DefaultHTTPClient returns an *http.Client tuned for concurrent agent use.
func DefaultHTTPClient() *http.Client {
	tr, ok := http.DefaultTransport.(*http.Transport)
	if ok {
		tr = tr.Clone()
	} else {
		tr = &http.Transport{}
	}
	tr.MaxIdleConnsPerHost = 64
	return &http.Client{Transport: tr}
}

// DefaultMaxTokens is the output ceiling every adapter applies when Request.MaxTokens is 0.
const DefaultMaxTokens = 4096

// ErrStreamIncomplete marks a stream that ended without a terminal stop signal.
var ErrStreamIncomplete = errors.New("golm: stream ended without a terminal event")

// SyntheticToolID builds a stable placeholder tool-call id for providers whose wire format omits one.
func SyntheticToolID(index int) string { return fmt.Sprintf("call_%d", index) }

// ProviderError is a non-2xx wire response from a provider.
type ProviderError struct {
	Provider string
	Status   int
	Body     string
	Err      error
}

func (e *ProviderError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("%s: status %d: %s", e.Provider, e.Status, e.Body)
	}
	return fmt.Sprintf("%s: status %d", e.Provider, e.Status)
}

func (e *ProviderError) Unwrap() error { return e.Err }

// Retryable reports whether the status is worth retrying.
func (e *ProviderError) Retryable() bool {
	return e.Status == 429 || (e.Status >= 500 && e.Status <= 599)
}

// StatusOf returns the HTTP status carried by a ProviderError in err's chain, or 0 if there is none.
func StatusOf(err error) int {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.Status
	}
	return 0
}

// Provider is an LLM backend.
type Provider interface {
	Name() string

	Capabilities() Capabilities

	Complete(ctx context.Context, req Request) (Response, error)

	Stream(ctx context.Context, req Request, fn func(StreamEvent) error) (Response, error)
}

// Capabilities describes a provider's optional feature support.
type Capabilities struct {
	Streaming bool
	Tools     bool
	Thinking  bool
	Images    bool
	Audio     bool

	PromptCaching      bool
	Effort             bool
	Safety             bool
	ResponseModalities bool

	ToolResultImages bool
}

// Effort is how hard the model should work on a request.
type Effort string

const (
	EffortNone   Effort = ""
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
	EffortXHigh  Effort = "xhigh"
	EffortMax    Effort = "max"
)

// ThinkingMode selects how a provider should allocate reasoning effort.
type ThinkingMode int

const (
	ThinkingOff ThinkingMode = iota
	ThinkingAuto

	// ThinkingBudget bounds thinking with a fixed token budget.
	ThinkingBudget

	// ThinkingDisabled turns extended thinking OFF explicitly.
	ThinkingDisabled
)

// ThinkingConfig configures extended thinking for a Request.
type ThinkingConfig struct {
	Mode   ThinkingMode
	Budget int
}

// ToolDef is the provider-facing definition of a tool.
type ToolDef struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

// CacheTTL selects how long a cached prefix stays alive.
type CacheTTL string

const (
	CacheTTLDefault CacheTTL = ""
	CacheTTL1h      CacheTTL = "1h"
)

// CacheConfig asks the provider to cache stable spans of the prompt.
type CacheConfig struct {
	MessagePrefix int

	TTL CacheTTL
}

// Request is a neutral completion request.
type Request struct {
	Model string

	System   SystemPrompt
	Messages []Message
	Tools    []ToolDef

	ToolChoice string

	MaxTokens int

	Temperature *float64
	Thinking    ThinkingConfig

	Effort Effort

	Cache CacheConfig

	ResponseModalities []string

	Safety SafetyLevel
}

// SafetyLevel selects how aggressively the provider blocks on content safety.
type SafetyLevel string

const (
	// SafetyDefault leaves the provider's own thresholds in place.
	SafetyDefault SafetyLevel = ""
	// SafetyLowered blocks only high-confidence harm.
	SafetyLowered SafetyLevel = "low"
	// SafetyNone disables safety blocking where the provider permits it.
	SafetyNone SafetyLevel = "none"
)

// StopReason explains why a completion ended.
type StopReason string

const (
	StopEndTurn   StopReason = "end_turn"
	StopToolUse   StopReason = "tool_use"
	StopMaxTokens StopReason = "max_tokens"

	// StopRefusal is the model declining, reported as a normal 200 response with little or no content.
	StopRefusal StopReason = "refusal"

	// StopContextOverflow is the CONTEXT window exhausted, as distinct from the requested output cap.
	StopContextOverflow StopReason = "context_overflow"

	// StopPause is a server-side tool loop hitting its iteration limit.
	StopPause StopReason = "pause"
	StopOther StopReason = "other"
)

// Usage reports token accounting for a completion.
type Usage struct {
	InputTokens  int
	OutputTokens int

	ThinkingTokens   int
	CacheReadTokens  int
	CacheWriteTokens int

	CacheWrite5mTokens int
	CacheWrite1hTokens int

	LostCompletions int
}

// Merge folds one CUMULATIVE usage frame into u.
func (u *Usage) Merge(o Usage) {
	pick := func(a, b int) int {
		if b > a {
			return b
		}
		return a
	}
	u.InputTokens = pick(u.InputTokens, o.InputTokens)
	u.OutputTokens = pick(u.OutputTokens, o.OutputTokens)
	u.ThinkingTokens = pick(u.ThinkingTokens, o.ThinkingTokens)
	u.CacheReadTokens = pick(u.CacheReadTokens, o.CacheReadTokens)
	u.CacheWriteTokens = pick(u.CacheWriteTokens, o.CacheWriteTokens)
	u.CacheWrite5mTokens = pick(u.CacheWrite5mTokens, o.CacheWrite5mTokens)
	u.CacheWrite1hTokens = pick(u.CacheWrite1hTokens, o.CacheWrite1hTokens)

	if u.CacheWrite5mTokens+u.CacheWrite1hTokens > u.CacheWriteTokens {
		u.CacheWrite5mTokens, u.CacheWrite1hTokens = 0, 0
	}
}

// Add accumulates o into u.
func (u *Usage) Add(o Usage) {
	u.InputTokens += o.InputTokens
	u.OutputTokens += o.OutputTokens
	u.ThinkingTokens += o.ThinkingTokens
	u.CacheReadTokens += o.CacheReadTokens
	u.CacheWriteTokens += o.CacheWriteTokens
	u.CacheWrite5mTokens += o.CacheWrite5mTokens
	u.CacheWrite1hTokens += o.CacheWrite1hTokens
	u.LostCompletions += o.LostCompletions
}

// Total returns input+output+thinking tokens.
func (u Usage) Total() int { return u.InputTokens + u.OutputTokens + u.ThinkingTokens }

// String renders the usage on one line, omitting thinking and cache counts when zero.
func (u Usage) String() string {
	s := fmt.Sprintf("input %d, output %d", u.InputTokens, u.OutputTokens)
	if u.ThinkingTokens > 0 {
		s += fmt.Sprintf(", thinking %d", u.ThinkingTokens)
	}
	if u.CacheReadTokens > 0 || u.CacheWriteTokens > 0 {
		s += fmt.Sprintf(", cache read %d, cache write %d", u.CacheReadTokens, u.CacheWriteTokens)

		if u.CacheWrite5mTokens > 0 || u.CacheWrite1hTokens > 0 {
			s += fmt.Sprintf(" (5m %d, 1h %d)", u.CacheWrite5mTokens, u.CacheWrite1hTokens)
		}
	}
	s += fmt.Sprintf(", total %d", u.Total())

	if u.LostCompletions > 0 {
		s += fmt.Sprintf(" (+%d delivered request(s) whose response was lost — billed, tokens unknown)", u.LostCompletions)
	}
	return s
}

// Response is the result of a completion.
type Response struct {
	Message    Message
	StopReason StopReason
	Usage      Usage

	ID string

	Raw json.RawMessage
}

// StreamEventType classifies a normalised streaming event.
type StreamEventType string

const (
	EventTextDelta     StreamEventType = "text_delta"
	EventThinkingDelta StreamEventType = "thinking_delta"
	EventToolStart     StreamEventType = "tool_start"
	EventToolDelta     StreamEventType = "tool_delta"
	EventToolStop      StreamEventType = "tool_stop"

	// EventToolResult is the tool having RUN.
	EventToolResult StreamEventType = "tool_result"
	EventDone       StreamEventType = "done"

	// EventAgentStart and EventAgentStop bracket a DELEGATION.
	EventAgentStart StreamEventType = "agent_start"
	EventAgentStop  StreamEventType = "agent_stop"
)

// StreamEvent is a single normalised incremental event from Stream.
type StreamEvent struct {
	Type     StreamEventType
	Text     string
	ToolID   string
	ToolName string
	ToolArgs string

	ToolError bool
	Usage     *Usage

	Agent string

	Depth int
}
