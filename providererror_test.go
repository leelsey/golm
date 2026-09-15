// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"errors"
	"io"
	"testing"
)

// Unwrap is what makes errors.Is and errors.As reach the cause a provider wrapped.
func TestProviderErrorUnwrapsToItsCause(t *testing.T) {
	cause := io.ErrUnexpectedEOF
	err := error(&ProviderError{Provider: "anthropic", Status: 502, Body: "bad gateway", Err: cause})

	if !errors.Is(err, cause) {
		t.Error("errors.Is could not reach the wrapped cause")
	}
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Status != 502 {
		t.Errorf("errors.As did not yield the ProviderError: %+v", pe)
	}
	if got := errors.Unwrap(err); got != cause {
		t.Errorf("errors.Unwrap = %v, want %v", got, cause)
	}
}

// A ProviderError with no cause must unwrap to nil rather than to itself, or errors.Is walks for ever.
func TestProviderErrorWithNoCauseUnwrapsToNil(t *testing.T) {
	err := &ProviderError{Provider: "openai", Status: 500}
	if got := errors.Unwrap(err); got != nil {
		t.Errorf("Unwrap = %v, want nil", got)
	}
	if errors.Is(err, io.EOF) {
		t.Error("a causeless ProviderError matched an unrelated error")
	}
}
