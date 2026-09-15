// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package netbus

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/leelsey/golm"
)

func waitSubs(t *testing.T, h *Hub, n int) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if h.SubscriberCount() >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("only %d/%d subscribers connected", h.SubscriberCount(), n)
}

func TestBridgeCrossesBusesOnceWithOrigin(t *testing.T) {
	hub := NewHub()
	srv := httptest.NewServer(hub.Handler())
	defer srv.Close()

	busA, busB := golm.NewBus(), golm.NewBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go NewClient(srv.URL).Bridge(ctx, busA)
	go NewClient(srv.URL).Bridge(ctx, busB)
	waitSubs(t, hub, 2)

	evCh, unsub := busB.Subscribe("*")
	defer unsub()

	busA.Publish(ctx, golm.Event{Topic: "agentX", Agent: "agentX", Kind: "done", Data: "hi"})

	select {
	case got := <-evCh:
		if got.Kind != "done" || got.Agent != "agentX" {
			t.Errorf("crossed event = %+v", got)
		}
		if got.Origin == "" {
			t.Errorf("injected event must carry a non-empty origin (loop guard)")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("event did not cross from busA to busB")
	}

	select {
	case extra := <-evCh:
		t.Errorf("unexpected duplicate/looped event: %+v", extra)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestBridgeTopicFilter(t *testing.T) {
	hub := NewHub()
	srv := httptest.NewServer(hub.Handler())
	defer srv.Close()

	busA, busB := golm.NewBus(), golm.NewBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go NewClient(srv.URL).Bridge(ctx, busA)
	go NewClient(srv.URL).Bridge(ctx, busB, "keep")
	waitSubs(t, hub, 2)

	evCh, unsub := busB.Subscribe("*")
	defer unsub()

	busA.Publish(ctx, golm.Event{Topic: "drop", Kind: "x"})
	busA.Publish(ctx, golm.Event{Topic: "keep", Kind: "y"})

	select {
	case got := <-evCh:
		if got.Topic != "keep" {
			t.Errorf("topic filter leaked: got %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("filtered event 'keep' did not arrive")
	}
}
