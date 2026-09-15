// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package anthropic

import (
	"fmt"
	"testing"

	"github.com/leelsey/golm"
)

// An Anthropic UpstreamError must classify through the neutral seam the same as the other adapters' *golm.ProviderError.
func TestUpstreamErrorClassifiesAsProviderError(t *testing.T) {
	err := error(&UpstreamError{Status: 429, Body: "slow down"})
	if got := golm.StatusOf(err); got != 429 {
		t.Fatalf("golm.StatusOf = %d, want 429", got)
	}

	wrapped := fmt.Errorf("agent: %w", err)
	if got := golm.StatusOf(wrapped); got != 429 {
		t.Fatalf("wrapped StatusOf = %d, want 429", got)
	}
	if golm.StatusOf(&UpstreamError{Status: 400}) != 400 {
		t.Error("400 should surface as 400")
	}
}
