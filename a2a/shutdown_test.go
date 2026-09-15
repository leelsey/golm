// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package a2a

import (
	"context"
	"testing"
	"time"
)

type blockTillCancel struct{ released chan struct{} }

func (h blockTillCancel) HandleMessage(ctx context.Context, _ Message) ([]Part, error) {
	<-ctx.Done()
	close(h.released)
	return nil, ctx.Err()
}

func TestTaskStoreShutdownCancelsAsync(t *testing.T) {
	st := NewTaskStore()
	h := blockTillCancel{released: make(chan struct{})}
	st.SendAsync(h, Message{})

	done := make(chan struct{})
	go func() { st.Shutdown(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not return")
	}
	select {
	case <-h.released:
	default:
		t.Error("async handler was not cancelled by Shutdown")
	}
}

func TestBrokerBufferCap(t *testing.T) {
	b := newTaskBroker(4)
	for i := 0; i < 20; i++ {
		b.publish(taskEvent{kind: "status"})
	}
	b.mu.Lock()
	n := len(b.buf)
	b.mu.Unlock()
	if n > 4 {
		t.Errorf("replay buffer not capped: len=%d, want <= 4", n)
	}
}
