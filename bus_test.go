// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"testing"
)

func TestBusTopicFiltering(t *testing.T) {
	b := NewBus()
	all, unsubAll := b.Subscribe("*")
	mainCh, unsubMain := b.Subscribe("main")
	defer unsubAll()
	defer unsubMain()

	ctx := context.Background()
	b.Publish(ctx, Event{Topic: "main", Agent: "main", Kind: "start"})
	b.Publish(ctx, Event{Topic: "sub", Agent: "sub", Kind: "done"})

	if ev := <-all; ev.Agent != "main" {
		t.Errorf("all[0] = %+v", ev)
	}
	if ev := <-all; ev.Agent != "sub" {
		t.Errorf("all[1] = %+v", ev)
	}
	if ev := <-mainCh; ev.Kind != "start" {
		t.Errorf("main = %+v", ev)
	}
	select {
	case ev := <-mainCh:
		t.Errorf("topic 'main' leaked an event: %+v", ev)
	default:
	}
}

func TestBusPublishIgnoresCancelledContext(t *testing.T) {
	b := NewBus()
	ch, unsub := b.Subscribe("*")
	defer unsub()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b.Publish(ctx, Event{Topic: "x", Kind: "k"})

	select {
	case ev := <-ch:
		if ev.Kind != "k" {
			t.Errorf("got %+v", ev)
		}
	default:
		t.Fatal("event dropped despite free buffer and cancelled ctx")
	}
}

func TestBusSubscribeAfterCloseReturnsClosedChannel(t *testing.T) {
	b := NewBus()
	b.Close()
	ch, unsub := b.Subscribe("*")
	defer unsub()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected a closed channel after Close")
		}
	default:
		t.Fatal("subscribe-after-close channel is not closed (would hang a range)")
	}
}

func TestBusClose(t *testing.T) {
	b := NewBus()
	ch, _ := b.Subscribe("*")
	b.Close()
	if _, ok := <-ch; ok {
		t.Errorf("expected channel closed after Close")
	}
	b.Publish(context.Background(), Event{Kind: "x"})
}
