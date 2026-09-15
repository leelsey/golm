// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
)

// Event is a message published on a Bus.
type Event struct {
	Topic string
	Agent string
	Kind  string
	Data  any

	Origin string
}

// StepEvent is the Data of a "step" event, one per provider call in a run.
type StepEvent struct {
	Step       int
	StopReason StopReason
	Usage      Usage
}

// ToolCallEvent is the Data of a "tool" event, published before execution.
type ToolCallEvent struct {
	Name  string
	ID    string
	Input json.RawMessage
}

// ToolResultEvent is the Data of a "tool_result" event, published after execution.
type ToolResultEvent struct {
	Name    string
	ID      string
	IsError bool
	Content string
}

// Bus is a lightweight in-process publish/subscribe hub built on channels.
type Bus struct {
	mu      sync.RWMutex
	subs    map[int]subscription
	nextID  int
	closed  bool
	dropped atomic.Uint64
}

type subscription struct {
	topic string
	ch    chan Event
}

// NewBus returns an empty Bus.
func NewBus() *Bus {
	return &Bus{subs: make(map[int]subscription)}
}

// Subscribe returns a channel of events matching topic.
func (b *Bus) Subscribe(topic string) (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		ch := make(chan Event)
		close(ch)
		return ch, func() {}
	}
	id := b.nextID
	b.nextID++
	ch := make(chan Event, 64)
	b.subs[id] = subscription{topic: topic, ch: ch}
	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if s, ok := b.subs[id]; ok {
			close(s.ch)
			delete(b.subs, id)
		}
	}
}

// Publish delivers ev to every matching subscriber without blocking.
func (b *Bus) Publish(ctx context.Context, ev Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return
	}
	for _, s := range b.subs {
		if s.topic != "" && s.topic != "*" && s.topic != ev.Topic {
			continue
		}
		select {
		case s.ch <- ev:
		default:
			b.dropped.Add(1)
		}
	}
}

// Close closes all subscriber channels.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for id, s := range b.subs {
		close(s.ch)
		delete(b.subs, id)
	}
}

// Dropped reports how many events were dropped because a subscriber's buffer was full.
func (b *Bus) Dropped() uint64 { return b.dropped.Load() }
