// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package netbus

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leelsey/golm"
)

func TestReceiveReconnectsAndResumes(t *testing.T) {
	var mu sync.Mutex
	var lastIDs []string
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		lastIDs = append(lastIDs, r.Header.Get("Last-Event-ID"))
		mu.Unlock()
		w.Header().Set("content-type", "text/event-stream")
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Error("no flusher")
			return
		}
		if atomic.AddInt32(&calls, 1) == 1 {
			fmt.Fprintf(w, "data: %s\n\n", `{"id":5,"topic":"t","agent":"a","kind":"k","origin":"other"}`)
			fl.Flush()
			return
		}
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

	got := map[string]bool{}
	deadline := time.After(3 * time.Second)
	for len(got) < 2 {
		select {
		case ev := <-events:
			got[ev.Kind] = true
		case <-deadline:
			t.Fatalf("only received %v across reconnect", got)
		}
	}
	cancel()
	mu.Lock()
	defer mu.Unlock()
	resumed := false
	for _, id := range lastIDs {
		if id == "5" {
			resumed = true
		}
	}
	if !resumed {
		t.Errorf("reconnect did not carry Last-Event-ID 5; saw %v", lastIDs)
	}
}
