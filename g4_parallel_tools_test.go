// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMaxParallelToolsBounded(t *testing.T) {
	const limit = 2
	var live, peak int64
	var mu sync.Mutex

	tool := NewTool("slow", "", json.RawMessage(`{"type":"object"}`),
		func(_ context.Context, _ json.RawMessage) (string, error) {
			n := atomic.AddInt64(&live, 1)
			mu.Lock()
			if n > peak {
				peak = n
			}
			mu.Unlock()
			time.Sleep(10 * time.Millisecond)
			atomic.AddInt64(&live, -1)
			return "ok", nil
		})

	uses := make([]Content, 6)
	for i := range uses {
		uses[i] = ToolUse{ID: string(rune('a' + i)), Name: "slow", Input: json.RawMessage(`{}`)}
	}
	fp := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: uses}, StopReason: StopToolUse},
		{Message: AssistantText("done"), StopReason: StopEndTurn},
	}}
	reg := NewRegistry()
	reg.Register(tool)
	a := &Agent{Provider: fp, Model: "x", Tools: reg,
		ParallelTools: true, MaxParallelTools: limit}

	if _, err := a.Run(context.Background(), NewSession(), "hi"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if peak > limit {
		t.Fatalf("peak concurrency %d exceeded limit %d", peak, limit)
	}
	if peak < 2 {
		t.Fatalf("peak concurrency %d shows tools did not run in parallel", peak)
	}
}
