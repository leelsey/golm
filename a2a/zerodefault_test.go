// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package a2a

import "testing"

func TestZeroRetentionLimitsMeanDefaults(t *testing.T) {
	st := NewTaskStore()
	st.MaxAge = 0
	st.MaxTasks = 0

	tk := st.newTask(Message{})
	st.complete(tk, []Part{TextPart("done")})
	st.store(tk)

	if _, ok := st.Get(tk.ID); !ok {
		t.Fatal("fresh terminal task evicted under zero limits (zero must mean default, not zero retention)")
	}
}
