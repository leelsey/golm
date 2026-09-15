// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// DelegationScope decides what a sub-agent remembers between the calls of one conversation.
type DelegationScope string

const (
	// ScopeConversation gives each sub-agent ONE session per conversation, reused across every delegation in it.
	ScopeConversation DelegationScope = "conversation"

	// ScopeCall gives every call a fresh session.
	ScopeCall DelegationScope = "call"
)

// DefaultMaxConversations bounds how many conversations an Orchestrator keeps sub-agent sessions for.
const DefaultMaxConversations = 128

type conversationKey struct{}

func conversationOf(ctx context.Context) string {
	id, _ := ctx.Value(conversationKey{}).(string)
	return id
}

func withConversation(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, conversationKey{}, id)
}

type agentSlot struct {
	sess *Session
	gate chan struct{}
}

func newAgentSlot() *agentSlot {
	return &agentSlot{sess: NewSession(), gate: make(chan struct{}, 1)}
}

func (a *agentSlot) acquire(ctx context.Context) error {
	select {
	case a.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *agentSlot) release() { <-a.gate }

type conversation struct {
	mu    sync.Mutex
	slots map[string]*agentSlot
}

func (c *conversation) slot(agent string) *agentSlot {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.slots == nil {
		c.slots = map[string]*agentSlot{}
	}
	s, ok := c.slots[agent]
	if !ok {
		s = newAgentSlot()
		c.slots[agent] = s
	}
	return s
}

func (c *conversation) sessions() map[string]*Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]*Session, len(c.slots))
	for name, s := range c.slots {
		out[name] = s.sess
	}
	return out
}

func (o *Orchestrator) scope() DelegationScope {
	if o.Scope == ScopeCall {
		return ScopeCall
	}
	return ScopeConversation
}

func (o *Orchestrator) maxConversations() int {
	if o.MaxConversations > 0 {
		return o.MaxConversations
	}
	return DefaultMaxConversations
}

func (o *Orchestrator) conversationFor(id string) *conversation {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.convs == nil {
		o.convs = map[string]*conversation{}
	}
	if c, ok := o.convs[id]; ok {
		o.touchLocked(id)
		return c
	}
	c := &conversation{}
	o.convs[id] = c
	o.order = append(o.order, id)
	o.evictLocked()
	return c
}

func (o *Orchestrator) evictLocked() {
	limit := o.maxConversations()

	for attempts := len(o.order); attempts > 0 && len(o.order) > limit; attempts-- {
		oldest := o.order[0]
		o.order = o.order[1:]
		if c := o.convs[oldest]; c != nil && c.busy() {
			o.order = append(o.order, oldest)
			continue
		}
		delete(o.convs, oldest)
	}
}

func (c *conversation) busy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, slot := range c.slots {
		if len(slot.gate) > 0 {
			return true
		}
	}
	return false
}

func (o *Orchestrator) touchLocked(id string) {
	for i, v := range o.order {
		if v == id {
			o.order = append(append(o.order[:i:i], o.order[i+1:]...), id)
			return
		}
	}
	o.order = append(o.order, id)
}

// Forget drops the sub-agent sessions kept for a conversation.
func (o *Orchestrator) Forget(conversationID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.convs, conversationID)
	for i, v := range o.order {
		if v == conversationID {
			o.order = append(o.order[:i], o.order[i+1:]...)
			break
		}
	}
}

// Sessions returns the sub-agent sessions of one conversation, by agent name.
func (o *Orchestrator) Sessions(conversationID string) map[string]*Session {
	o.mu.RLock()
	c := o.convs[conversationID]
	o.mu.RUnlock()
	if c == nil {
		return nil
	}
	return c.sessions()
}

func (o *Orchestrator) sessionFor(ctx context.Context, agent string) (*Session, func(), error) {
	id := conversationOf(ctx)
	if o.scope() == ScopeCall || id == "" {
		return NewSession(), func() {}, nil
	}
	slot := o.conversationFor(id).slot(agent)
	if err := slot.acquire(ctx); err != nil {
		return nil, nil, fmt.Errorf("orchestrator: waiting for %q: %w", agent, err)
	}
	return slot.sess, slot.release, nil
}

func (o *Orchestrator) delegate(ctx context.Context, name, task string) (string, error) {
	sub, ok := o.Get(name)
	if !ok {
		return "", fmt.Errorf("orchestrator: unknown agent %q", name)
	}
	depth := delegationDepth(ctx) + 1
	if limit := o.maxDepth(); depth > limit {
		return "", fmt.Errorf("%w: %q is %d levels down, past the limit of %d; answer directly instead",
			ErrDelegationTooDeep, name, depth, limit)
	}
	ctx = context.WithValue(ctx, delegationDepthKey{}, depth)

	sess, release, err := o.sessionFor(ctx, name)
	if err != nil {
		return "", err
	}
	defer release()

	res, err := o.runSub(ctx, sub, sess, name, task, depth)

	o.addUsage(res.Usage)
	if err != nil {
		return "", err
	}
	return res.Text(), nil
}

func (o *Orchestrator) runSub(ctx context.Context, sub *Agent, sess *Session, name, task string, depth int) (Result, error) {
	out := streamSinkOf(ctx)
	_ = out.emit(StreamEvent{Type: EventAgentStart, Agent: name, Depth: depth, Text: task})
	defer func() { _ = out.emit(StreamEvent{Type: EventAgentStop, Agent: name, Depth: depth}) }()

	if out == nil || !o.StreamDelegates {
		return sub.Run(withoutStreamSink(ctx), sess, task)
	}
	return sub.Stream(ctx, sess, task, func(ev StreamEvent) error {
		if ev.Agent == "" {
			ev.Agent = name
		}
		if ev.Depth == 0 {
			ev.Depth = depth
		}
		return out.emit(ev)
	})
}

const subSessionsKey = "golm.sub_sessions"

// Restore puts previously saved sub-agent sessions back into a conversation.
func (o *Orchestrator) Restore(conversationID string, sessions map[string]*Session) {
	if conversationID == "" || len(sessions) == 0 {
		return
	}
	c := o.conversationFor(conversationID)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.slots == nil {
		c.slots = map[string]*agentSlot{}
	}
	for name, s := range sessions {
		if s == nil {
			continue
		}

		if cur, ok := c.slots[name]; ok && len(cur.gate) > 0 {
			continue
		}
		c.slots[name] = &agentSlot{sess: s, gate: make(chan struct{}, 1)}
	}
}

// SaveConversation persists the whole conversation.
func (o *Orchestrator) SaveConversation(ctx context.Context, store SessionStore, main *Session) error {
	if store == nil || main == nil {
		return nil
	}
	subs := o.Sessions(main.ID())
	ids := make(map[string]string, len(subs))
	for name, s := range subs {
		if err := store.Save(ctx, s); err != nil {
			return fmt.Errorf("orchestrator: saving %q's session: %w", name, err)
		}
		ids[name] = s.ID()
	}
	if len(ids) > 0 {
		if err := main.SetState(subSessionsKey, ids); err != nil {
			return fmt.Errorf("orchestrator: recording sub-agent sessions: %w", err)
		}
	}
	return store.Save(ctx, main)
}

// LoadConversation restores the sub-agent sessions recorded against main.
func (o *Orchestrator) LoadConversation(ctx context.Context, store SessionStore, main *Session) (int, error) {
	if store == nil || main == nil {
		return 0, nil
	}
	ids, ok, err := GetState[map[string]string](main, subSessionsKey)
	if err != nil {
		return 0, fmt.Errorf("orchestrator: reading sub-agent session ids: %w", err)
	}
	if !ok || len(ids) == 0 {
		return 0, nil
	}
	restored := make(map[string]*Session, len(ids))
	for name, id := range ids {
		s, err := store.Load(ctx, id)
		if err != nil {
			if errors.Is(err, ErrSessionNotFound) {
				continue
			}
			return 0, fmt.Errorf("orchestrator: loading %q's session %s: %w", name, id, err)
		}
		restored[name] = s
	}
	o.Restore(main.ID(), restored)
	return len(restored), nil
}
