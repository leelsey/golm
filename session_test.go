// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"sync"
	"testing"
)

// State writes and snapshots race.
func TestSessionConcurrentStateAndSave(t *testing.T) {
	s := NewSession()
	s.Append(UserText("hi"))
	store := newMemStore()
	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			if err := s.SetState("k", i); err != nil {
				t.Errorf("set state: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			if err := store.Save(ctx, s); err != nil {
				t.Errorf("save: %v", err)
				return
			}
		}
	}()
	wg.Wait()
}
