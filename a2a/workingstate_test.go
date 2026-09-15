// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package a2a

import (
	"context"
	"testing"
	"time"
)

func TestStoredStateIsWorkingDuringRun(t *testing.T) {
	st := NewTaskStore()
	h := &blockingHandler{started: make(chan struct{}), returned: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan Task, 1)
	go func() {
		task, _ := st.Send(ctx, h, Message{Parts: []Part{TextPart("hi")}})
		done <- task
	}()
	<-h.started

	tasks := st.List()
	if len(tasks) != 1 {
		t.Fatalf("store holds %d tasks, want 1", len(tasks))
	}
	if got := tasks[0].Status.State; got != StateWorking {
		t.Fatalf("stored state during run = %q, want %q", got, StateWorking)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Send did not return after cancel")
	}
}
