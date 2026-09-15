// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package netbus

import (
	"sync"
	"testing"
)

func TestRingIDOrderMatchesBufferOrderUnderConcurrency(t *testing.T) {
	const goroutines, each = 50, 20
	r := newRing(goroutines * each)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if ev := r.add(Event{Topic: "t"}); ev.ID == 0 {
					t.Error("add returned an event without an ID")
				}
			}
		}()
	}
	wg.Wait()

	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.buf) != goroutines*each {
		t.Fatalf("buffer holds %d events, want %d", len(r.buf), goroutines*each)
	}
	for i, ev := range r.buf {
		if ev.ID != int64(i+1) {
			t.Fatalf("buf[%d].ID = %d, want %d (buffer order diverged from ID order)", i, ev.ID, i+1)
		}
	}
}
