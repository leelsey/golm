// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package a2a

import (
	"context"
	"sync"
	"testing"
)

type recordingSink struct {
	mu     sync.Mutex
	calls  []string
	taskID string
}

func (r *recordingSink) OnTask(taskID, _ string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls, r.taskID = append(r.calls, "OnTask"), taskID
}

func (r *recordingSink) Status(TaskStatus, bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, "Status")
	return nil
}

func (r *recordingSink) Artifact(Artifact, bool, bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, "Artifact")
	return nil
}

// StreamSink is exported, so its documented order.
func TestStreamSinkIsToldTheTaskBeforeAnythingElse(t *testing.T) {
	sink := &recordingSink{}
	st := NewTaskStore()
	task := st.SendStream(context.Background(), echoHandler{},
		Message{Role: "user", Parts: []Part{TextPart("hi")}}, sink)

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.calls) == 0 {
		t.Fatal("the sink was never driven")
	}
	if sink.calls[0] != "OnTask" {
		t.Errorf("first call was %q, want OnTask — the sink learns the task identity last", sink.calls[0])
	}
	for _, c := range sink.calls[1:] {
		if c == "OnTask" {
			t.Error("OnTask was called more than once; the contract says once")
		}
	}
	if sink.taskID != task.ID {
		t.Errorf("OnTask reported task %q, want %q", sink.taskID, task.ID)
	}
}
