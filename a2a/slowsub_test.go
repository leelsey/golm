// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package a2a

import (
	"context"
	"sync"
	"testing"
	"time"
)

type floodHandler struct {
	started chan struct{}
	emitGo  chan struct{}
	n       int
}

func (floodHandler) HandleMessage(context.Context, Message) ([]Part, error) {
	return []Part{TextPart("final")}, nil
}

func (h floodHandler) HandleMessageStream(_ context.Context, _ Message, emit func(string) error) ([]Part, error) {
	close(h.started)
	<-h.emitGo
	for i := 0; i < h.n; i++ {
		_ = emit("x")
	}
	return []Part{TextPart("final")}, nil
}

type gateSink struct {
	mu        sync.Mutex
	once      sync.Once
	blocked   chan struct{}
	gate      chan struct{}
	finals    int
	lastState TaskState
}

func (s *gateSink) OnTask(string, string) {}

func (s *gateSink) firstBlock() {
	s.once.Do(func() {
		close(s.blocked)
		<-s.gate
	})
}

func (s *gateSink) Status(st TaskStatus, final bool) error {
	s.firstBlock()
	if final {
		s.mu.Lock()
		s.finals++
		s.lastState = st.State
		s.mu.Unlock()
	}
	return nil
}

func (s *gateSink) Artifact(Artifact, bool, bool) error {
	s.firstBlock()
	return nil
}

func TestSubscribeSlowSubscriberGetsFinalSnapshot(t *testing.T) {
	st := NewTaskStore()
	h := floodHandler{started: make(chan struct{}), emitGo: make(chan struct{}), n: 200}
	task := st.SendAsync(h, Message{})
	<-h.started

	sink := &gateSink{blocked: make(chan struct{}), gate: make(chan struct{})}
	subDone := make(chan struct{})
	go func() {
		st.Subscribe(context.Background(), task.ID, sink)
		close(subDone)
	}()

	<-sink.blocked
	close(h.emitGo)

	if !func() bool {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if got, ok := st.Get(task.ID); ok && got.Status.State == StateCompleted {
				return true
			}
			time.Sleep(5 * time.Millisecond)
		}
		return false
	}() {
		t.Fatal("task never completed")
	}

	close(sink.gate)

	select {
	case <-subDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe did not return")
	}

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.finals == 0 {
		t.Fatal("slow subscriber dropped without a final status (silent truncation)")
	}
	if sink.lastState != StateCompleted {
		t.Errorf("final snapshot state = %q, want completed", sink.lastState)
	}
}
