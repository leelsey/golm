// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"errors"
	"testing"
)

func TestLoopHonoursCancelledContext(t *testing.T) {
	fp := &fakeProvider{responses: []Response{{Message: Message{Role: RoleAssistant}}}}
	a := &Agent{Provider: fp, Model: "x", Tools: NewRegistry()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := a.Run(ctx, NewSession(), "hi")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if fp.calls != 0 {
		t.Errorf("provider called %d times despite cancelled ctx", fp.calls)
	}
}

func TestCancelledRunDoesNotPoisonSession(t *testing.T) {
	fp := &fakeProvider{responses: []Response{{Message: Message{Role: RoleAssistant}}}}
	sess := NewSession()
	a := &Agent{Provider: fp, Model: "x", Tools: NewRegistry()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := a.Run(ctx, sess, "hi"); err == nil {
		t.Fatal("expected ctx error")
	}
	if n := len(sess.History()); n != 0 {
		t.Errorf("session has %d messages after cancelled run; want 0", n)
	}
}
