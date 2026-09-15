// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package netbus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/leelsey/golm"
)

// The bus is best-effort by design.
func TestPublishFailuresCountsLostPublishes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if got := c.PublishFailures(); got != 0 {
		t.Fatalf("a fresh client reports %d failures, want 0", got)
	}

	bus := golm.NewBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { _ = c.Bridge(ctx, bus); close(done) }()

	deadline := time.Now().Add(3 * time.Second)
	for c.PublishFailures() == 0 {
		bus.Publish(ctx, golm.Event{Topic: "t", Kind: "k"})
		if time.Now().After(deadline) {
			t.Fatal("a hub answering 500 produced no counted failure; loss towards the hub is invisible")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}

// Origin is how an event that came FROM the hub is recognised and not echoed back to it.
func TestOriginIsStableAndDistinct(t *testing.T) {
	a, b := NewClient("http://127.0.0.1:1"), NewClient("http://127.0.0.1:1")
	first, second := a.Origin(), a.Origin()
	if first == "" {
		t.Fatal("origin is empty; every event would look like it came from the hub")
	}
	if first != second {
		t.Errorf("origin changed between calls (%q then %q); loop prevention needs it stable", first, second)
	}
	if first == b.Origin() {
		t.Error("two clients share an origin; one would swallow the other's events")
	}
}
