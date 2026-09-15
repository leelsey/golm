// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package netbus

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leelsey/golm"
)

func TestReceiveDeduplicatesReplayedEvents(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Error("no flusher")
			return
		}
		if atomic.AddInt32(&calls, 1) == 1 {
			fmt.Fprintf(w, "data: %s\n\n", `{"id":5,"topic":"t","agent":"a","kind":"k1","origin":"other"}`)
			fl.Flush()
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", `{"id":5,"topic":"t","agent":"a","kind":"k1","origin":"other"}`)
		fmt.Fprintf(w, "data: %s\n\n", `{"id":6,"topic":"t","agent":"a","kind":"k2","origin":"other"}`)
		fl.Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	bus := golm.NewBus()
	events, unsub := bus.Subscribe("*")
	defer unsub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go NewClient(srv.URL).receive(ctx, bus, nil)

	counts := map[string]int{}
	deadline := time.After(3 * time.Second)
	for counts["k2"] == 0 {
		select {
		case ev := <-events:
			counts[ev.Kind]++
		case <-deadline:
			t.Fatalf("never saw the post-reconnect event; counts=%v", counts)
		}
	}
	if counts["k1"] != 1 {
		t.Errorf("replayed event delivered %d times, want exactly 1 (counts=%v)", counts["k1"], counts)
	}
}
