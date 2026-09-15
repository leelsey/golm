// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package netbus

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/leelsey/golm"
)

func TestBridgeOutboundTopicFilter(t *testing.T) {
	hub := NewHub()
	srv := httptest.NewServer(hub.Handler())
	defer srv.Close()

	busA, busB := golm.NewBus(), golm.NewBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go NewClient(srv.URL).Bridge(ctx, busA, "x")
	go NewClient(srv.URL).Bridge(ctx, busB)
	waitSubs(t, hub, 2)

	evCh, unsub := busB.Subscribe("*")
	defer unsub()

	busA.Publish(ctx, golm.Event{Topic: "y", Kind: "drop"})
	busA.Publish(ctx, golm.Event{Topic: "x", Kind: "keep"})

	select {
	case got := <-evCh:
		if got.Topic != "x" {
			t.Errorf("outbound topic filter leaked: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("filtered 'x' did not arrive")
	}
}

func TestConcurrentBroadcastAndDisconnect(t *testing.T) {
	hub := NewHub()
	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					hub.broadcast(Event{Topic: "t", Kind: "k"})
				}
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				id, _ := hub.addSub(nil)
				hub.removeSub(id)
			}
		}()
	}
	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()
}
