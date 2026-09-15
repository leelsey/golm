// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package a2a

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTaskEvictionByAge(t *testing.T) {
	st := NewTaskStore()
	st.MaxAge = time.Hour
	oldNow := nowFn
	defer func() { nowFn = oldNow }()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	nowFn = func() time.Time { return base.Add(-2 * time.Hour) }
	old := st.newTask(Message{})
	st.complete(old, []Part{TextPart("done")})
	st.store(old)

	nowFn = func() time.Time { return base }
	fresh := st.newTask(Message{})
	st.store(fresh)

	st.mu.Lock()
	_, oldKept := st.tasks[old.ID]
	_, freshKept := st.tasks[fresh.ID]
	st.mu.Unlock()
	if oldKept {
		t.Errorf("old terminal task should have been evicted")
	}
	if !freshKept {
		t.Errorf("fresh task should be retained")
	}
}

func TestTaskEvictionByCap(t *testing.T) {
	st := NewTaskStore()
	st.MaxTasks = 3
	for i := 0; i < 10; i++ {
		tk := st.newTask(Message{})
		st.complete(tk, nil)
		st.store(tk)
	}
	st.mu.Lock()
	n := len(st.tasks)
	st.mu.Unlock()
	if n > 3 {
		t.Errorf("task count = %d, want <= 3 (cap)", n)
	}
}

type blockingHandler struct {
	started  chan struct{}
	returned chan struct{}
}

func (h *blockingHandler) HandleMessage(ctx context.Context, _ Message) ([]Part, error) {
	close(h.started)
	<-ctx.Done()
	close(h.returned)
	return nil, ctx.Err()
}

func TestCancelInterruptsInFlight(t *testing.T) {
	h := &blockingHandler{started: make(chan struct{}), returned: make(chan struct{})}
	ts := httptest.NewServer(NewServer(AgentCard{}, h).HTTPHandler())
	defer ts.Close()

	go func() {
		body := `{"jsonrpc":"2.0","id":1,"method":"message/send","params":{"message":{"role":"user","parts":[{"kind":"text","text":"hi"}]}}}`
		if resp, err := http.Post(ts.URL+"/", "application/json", strings.NewReader(body)); err == nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-h.started:
	case <-time.After(2 * time.Second):
		t.Fatal("handler never started")
	}

	cancelBody := `{"jsonrpc":"2.0","id":2,"method":"tasks/cancel","params":{"id":"task-1"}}`
	resp, err := http.Post(ts.URL+"/", "application/json", strings.NewReader(cancelBody))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	select {
	case <-h.returned:
	case <-time.After(2 * time.Second):
		t.Fatal("tasks/cancel did not interrupt the in-flight handler")
	}
}
