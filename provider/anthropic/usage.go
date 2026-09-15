// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package anthropic

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/leelsey/golm"
)

// UsageHeader is how a proxy reports what a FAILED call spent.
const UsageHeader = "X-Gourd-Usage"

// UpstreamError is a non-2xx response, carrying whatever the proxy said the call had already cost.
type UpstreamError struct {
	Status int
	Body   string
	Usage  golm.Usage
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("anthropic: status %d: %s", e.Status, e.Body)
}

// Unwrap exposes a neutral *golm.ProviderError so golm.StatusOf / Retryable classify an Anthropic failure the same as the other adapters.
func (e *UpstreamError) Unwrap() error {
	return &golm.ProviderError{Provider: "anthropic", Status: e.Status, Body: e.Body}
}

// UsageFromHeader decodes the accounting a proxy attached to a failure.
func UsageFromHeader(h http.Header) golm.Usage {
	raw := h.Get(UsageHeader)
	if raw == "" {
		return golm.Usage{}
	}
	var u struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		CachedInputTokens   int `json:"cached_input_tokens"`
		CacheCreationTokens int `json:"cache_creation_tokens"`

		CacheWrite5mTokens int `json:"cache_write_5m_tokens"`
		ReasoningTokens    int `json:"reasoning_tokens"`
	}
	if json.Unmarshal([]byte(raw), &u) != nil {
		return golm.Usage{}
	}
	fiveMin, oneHour := cacheCreation{Ephemeral5mInputTokens: u.CacheWrite5mTokens}.split(u.CacheCreationTokens)
	if fiveMin > 0 {
		oneHour = u.CacheCreationTokens - fiveMin
	}

	return golm.Usage{
		InputTokens:        u.PromptTokens,
		OutputTokens:       u.CompletionTokens,
		ThinkingTokens:     u.ReasoningTokens,
		CacheReadTokens:    u.CachedInputTokens,
		CacheWriteTokens:   u.CacheCreationTokens,
		CacheWrite5mTokens: fiveMin,
		CacheWrite1hTokens: oneHour,
	}
}

// UsageFromError recovers the accounting a failed call reported, if any.
func UsageFromError(err error) golm.Usage {
	var ue *UpstreamError
	if errors.As(err, &ue) {
		return ue.Usage
	}
	return golm.Usage{}
}
