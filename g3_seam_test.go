// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"errors"
	"fmt"
	"testing"
)

func TestProviderErrorClassify(t *testing.T) {
	pe := &ProviderError{Provider: "openai", Status: 429, Body: "slow down"}
	if !pe.Retryable() {
		t.Error("429 should be retryable")
	}
	if (&ProviderError{Status: 400}).Retryable() {
		t.Error("400 should not be retryable")
	}
	if !(&ProviderError{Status: 503}).Retryable() {
		t.Error("503 should be retryable")
	}

	wrapped := fmt.Errorf("agent step: %w", pe)
	if StatusOf(wrapped) != 429 {
		t.Fatalf("StatusOf = %d, want 429", StatusOf(wrapped))
	}
	var got *ProviderError
	if !errors.As(wrapped, &got) || got.Status != 429 {
		t.Fatal("errors.As did not recover the ProviderError")
	}
	if StatusOf(errors.New("plain")) != 0 {
		t.Error("StatusOf of a plain error should be 0")
	}
}

func TestSyntheticToolIDIndexed(t *testing.T) {
	if SyntheticToolID(0) == SyntheticToolID(1) {
		t.Error("synthetic tool ids must differ by index")
	}
}

func TestDefaultHTTPClientNoWallClock(t *testing.T) {
	c := DefaultHTTPClient()
	if c.Timeout != 0 {
		t.Errorf("client.Timeout = %v, want 0 (wall-clock kills streams)", c.Timeout)
	}
	if c.Transport == nil {
		t.Error("client should carry its own transport, not the shared default")
	}
}

func TestErrStreamIncompleteWraps(t *testing.T) {
	err := fmt.Errorf("openai: stream ended: %w", ErrStreamIncomplete)
	if !errors.Is(err, ErrStreamIncomplete) {
		t.Error("wrapped stream error should match ErrStreamIncomplete")
	}
}
