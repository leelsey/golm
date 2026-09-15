// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestExitErr(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	live := context.Background()
	wrapped := fmt.Errorf("run: %w", context.Canceled)

	var errb strings.Builder
	if code := exitErr(&errb, cancelled, wrapped); code != 130 {
		t.Fatalf("signal cancel: code = %d, want 130", code)
	}
	if errb.Len() != 0 {
		t.Errorf("signal cancel: stderr = %q, want silence", errb.String())
	}

	if code := exitErr(&errb, live, wrapped); code != 1 {
		t.Fatalf("remote cancel with live ctx: code = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "context canceled") {
		t.Errorf("remote cancel: stderr = %q, want the error surfaced", errb.String())
	}

	errb.Reset()
	if code := exitErr(&errb, cancelled, errors.New("boom")); code != 1 {
		t.Fatalf("plain error: code = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "boom") {
		t.Errorf("plain error: stderr = %q, want it to mention the error", errb.String())
	}
}
