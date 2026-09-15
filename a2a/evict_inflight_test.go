// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package a2a

import (
	"testing"
	"time"
)

func TestEvictionSkipsInFlight(t *testing.T) {
	st := NewTaskStore()
	st.MaxAge = time.Hour
	oldNow := nowFn
	defer func() { nowFn = oldNow }()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	nowFn = func() time.Time { return base.Add(-2 * time.Hour) }
	tk := st.newTask(Message{})
	st.complete(tk, nil)
	st.mu.Lock()
	st.tasks[tk.ID] = tk
	st.inFlight[tk.ID] = func() {}
	st.mu.Unlock()

	nowFn = func() time.Time { return base }
	st.store(st.newTask(Message{}))

	st.mu.Lock()
	_, kept := st.tasks[tk.ID]
	st.mu.Unlock()
	if !kept {
		t.Error("in-flight task (cleanup pending) should not be evicted")
	}
}
