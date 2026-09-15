// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package a2a

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

var nowFn = time.Now

const (
	defaultMaxTaskAge      = time.Hour
	defaultMaxTasks        = 1024
	defaultMaxBrokerEvents = 4096
)

func now() string { return nowFn().UTC().Format(time.RFC3339) }

// Sentinel errors returned by Cancel.
var (
	ErrTaskNotFound      = errors.New("a2a: task not found")
	ErrTaskNotCancelable = errors.New("a2a: task cannot be canceled")
)

// StreamSink renders task lifecycle events to a transport's wire format.
type StreamSink interface {
	OnTask(taskID, contextID string)
	Status(s TaskStatus, final bool) error
	Artifact(a Artifact, appendChunk, lastChunk bool) error
}

// TaskStore is the transport-agnostic A2A task lifecycle engine, driven by both the HTTP.
type TaskStore struct {
	MaxAge          time.Duration
	MaxTasks        int
	MaxBrokerEvents int

	mu         sync.Mutex
	tasks      map[string]*Task
	inFlight   map[string]context.CancelFunc
	brokers    map[string]*taskBroker
	terminalAt map[string]time.Time
	wg         sync.WaitGroup
	seq        int64
}

// NewTaskStore returns an empty TaskStore.
func NewTaskStore() *TaskStore {
	return &TaskStore{
		MaxAge:          defaultMaxTaskAge,
		MaxTasks:        defaultMaxTasks,
		MaxBrokerEvents: defaultMaxBrokerEvents,
		tasks:           make(map[string]*Task),
		inFlight:        make(map[string]context.CancelFunc),
		brokers:         make(map[string]*taskBroker),
		terminalAt:      make(map[string]time.Time),
	}
}

func (s *TaskStore) newTask(msg Message) *Task {
	id := fmt.Sprintf("task-%d", atomic.AddInt64(&s.seq, 1))
	return &Task{ID: id, ContextID: msg.ContextID, Kind: "task",
		Status: TaskStatus{State: StateSubmitted, Timestamp: now()}, History: []Message{msg}}
}

func (s *TaskStore) complete(t *Task, parts []Part) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if isTerminal(t.Status.State) {
		return
	}
	t.Status = TaskStatus{State: StateCompleted, Timestamp: now()}
	t.Artifacts = []Artifact{{ArtifactID: t.ID + "-artifact", Parts: parts}}
	s.terminalAt[t.ID] = nowFn().Truncate(time.Second)
}

func (s *TaskStore) fail(t *Task, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if isTerminal(t.Status.State) {
		return
	}
	t.Status = TaskStatus{State: StateFailed, Timestamp: now(),
		Message: &Message{Role: "agent", Kind: "message", Parts: []Part{TextPart(err.Error())}}}
	s.terminalAt[t.ID] = nowFn().Truncate(time.Second)
}

func (s *TaskStore) store(t *Task) {
	s.mu.Lock()
	s.tasks[t.ID] = t
	s.evictLocked()
	s.mu.Unlock()
}

func (s *TaskStore) begin(ctx context.Context, task *Task) (context.Context, context.CancelFunc) {
	cctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	task.Status = TaskStatus{State: StateWorking, Timestamp: now()}
	s.tasks[task.ID] = task
	s.inFlight[task.ID] = cancel
	s.evictLocked()
	s.mu.Unlock()
	return cctx, cancel
}

func (s *TaskStore) end(id string) {
	s.mu.Lock()
	delete(s.inFlight, id)
	s.mu.Unlock()
}

func (s *TaskStore) snapshot(t *Task) Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	return *t
}

func (s *TaskStore) status(t *Task) TaskStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return t.Status
}

func (s *TaskStore) evictLocked() {
	maxAge, maxTasks := s.MaxAge, s.MaxTasks
	if maxAge <= 0 {
		maxAge = defaultMaxTaskAge
	}
	if maxTasks <= 0 {
		maxTasks = defaultMaxTasks
	}
	cutoff := nowFn().Add(-maxAge)
	for id, t := range s.tasks {
		if _, busy := s.inFlight[id]; busy {
			continue
		}
		if isTerminal(t.Status.State) {
			if ts, ok := s.terminalTime(id, t); ok && ts.Before(cutoff) {
				delete(s.tasks, id)
				delete(s.terminalAt, id)
			}
		}
	}
	if len(s.tasks) <= maxTasks {
		return
	}
	type aged struct {
		id string
		ts time.Time
	}
	var terms []aged
	for id, t := range s.tasks {
		if _, busy := s.inFlight[id]; busy {
			continue
		}
		if isTerminal(t.Status.State) {
			ts, _ := s.terminalTime(id, t)
			terms = append(terms, aged{id, ts})
		}
	}
	sort.Slice(terms, func(i, j int) bool { return terms[i].ts.Before(terms[j].ts) })
	for _, a := range terms {
		if len(s.tasks) <= maxTasks {
			break
		}
		delete(s.tasks, a.id)
		delete(s.terminalAt, a.id)
	}
}

func (s *TaskStore) terminalTime(id string, t *Task) (time.Time, bool) {
	if ts, ok := s.terminalAt[id]; ok {
		return ts, true
	}
	ts, err := time.Parse(time.RFC3339, t.Status.Timestamp)
	if err != nil {
		return time.Time{}, false
	}
	s.terminalAt[id] = ts
	return ts, true
}

// Send runs the handler synchronously and returns the final task plus the handler error.
func (s *TaskStore) Send(ctx context.Context, h Handler, msg Message) (Task, error) {
	task := s.newTask(msg)
	cctx, cancel := s.begin(ctx, task)
	defer cancel()
	defer s.end(task.ID)
	parts, err := h.HandleMessage(cctx, msg)
	if err != nil {
		s.fail(task, err)
	} else {
		s.complete(task, parts)
	}
	return s.snapshot(task), err
}

// SendStream runs the handler, rendering spec-ordered lifecycle events to sink.
func (s *TaskStore) SendStream(ctx context.Context, h Handler, msg Message, sink StreamSink) Task {
	task := s.newTask(msg)
	sink.OnTask(task.ID, task.ContextID)
	cctx, cancel := s.begin(ctx, task)
	defer cancel()
	defer s.end(task.ID)
	s.runStream(cctx, h, msg, task, sink)
	return s.snapshot(task)
}

func (s *TaskStore) runStream(ctx context.Context, h Handler, msg Message, task *Task, sink StreamSink) {
	_ = sink.Status(TaskStatus{State: StateWorking, Timestamp: now()}, false)

	firstChunk := true
	emit := func(text string) error {
		err := sink.Artifact(Artifact{ArtifactID: task.ID + "-artifact", Parts: []Part{TextPart(text)}}, !firstChunk, false)
		firstChunk = false
		return err
	}

	var parts []Part
	var err error
	if sh, ok := h.(StreamHandler); ok {
		parts, err = sh.HandleMessageStream(ctx, msg, emit)
	} else {
		parts, err = h.HandleMessage(ctx, msg)
	}
	if err != nil {
		s.fail(task, err)
		_ = sink.Status(s.status(task), true)
		return
	}
	s.complete(task, parts)
	snap := s.snapshot(task)
	if len(snap.Artifacts) > 0 {
		_ = sink.Artifact(snap.Artifacts[0], false, true)
	}
	_ = sink.Status(snap.Status, true)
}

// Get returns a snapshot of the stored task.
func (s *TaskStore) Get(id string) (Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return Task{}, false
	}
	return *t, true
}

// Cancel transitions a task to canceled and interrupts its handler.
func (s *TaskStore) Cancel(id string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return Task{}, ErrTaskNotFound
	}
	if isTerminal(t.Status.State) {
		return *t, ErrTaskNotCancelable
	}
	t.Status = TaskStatus{State: StateCanceled, Timestamp: now()}
	if fn := s.inFlight[id]; fn != nil {
		fn()
	}
	return *t, nil
}

// List returns a snapshot of every stored task.
func (s *TaskStore) List() []Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		out = append(out, *t)
	}
	return out
}

// SendAsync runs the handler detached and returns the working task at once.
func (s *TaskStore) SendAsync(h Handler, msg Message) Task {
	task := s.newTask(msg)
	ctx, cancel := s.begin(context.Background(), task)
	br := newTaskBroker(s.MaxBrokerEvents)
	s.mu.Lock()
	s.brokers[task.ID] = br
	working := *task
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		defer s.finishAsync(task.ID)
		sink := &brokerSink{br: br}

		sink.OnTask(task.ID, task.ContextID)
		s.runStream(ctx, h, msg, task, sink)
		br.close()
	}()
	return working
}

func (s *TaskStore) finishAsync(id string) {
	s.mu.Lock()
	delete(s.inFlight, id)
	delete(s.brokers, id)
	s.mu.Unlock()
}

// Shutdown cancels every in-flight task and waits.
func (s *TaskStore) Shutdown(ctx context.Context) {
	s.mu.Lock()
	for _, cancel := range s.inFlight {
		cancel()
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// Subscribe replays then live-streams a task's events to sink.
func (s *TaskStore) Subscribe(ctx context.Context, id string, sink StreamSink) bool {
	s.mu.Lock()
	br := s.brokers[id]
	s.mu.Unlock()
	if br == nil {
		return false
	}
	replay, ch, overflow, unsub := br.subscribe()
	defer unsub()
	for _, ev := range replay {
		if !emitEvent(sink, ev) {
			return true
		}
	}
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				if overflow.Load() {
					br.emitFinal(sink)
				}
				return true
			}
			if !emitEvent(sink, ev) {
				return true
			}
		case <-ctx.Done():
			return true
		}
	}
}

func (b *taskBroker) emitFinal(sink StreamSink) {
	b.mu.Lock()
	var status, terminal, artifact *taskEvent
	for i := len(b.buf) - 1; i >= 0; i-- {
		ev := b.buf[i]
		if status == nil && ev.kind == "status" {
			e := ev
			status = &e

			if isTerminal(ev.status.State) {
				terminal = &e
			}
		}
		if artifact == nil && ev.kind == "artifact" {
			e := ev
			artifact = &e
		}
		if status != nil && artifact != nil {
			break
		}
	}
	b.mu.Unlock()
	if artifact != nil {
		if sink.Artifact(artifact.artifact, artifact.appendChunk, artifact.lastChunk) != nil {
			return
		}
	}

	switch {
	case terminal != nil:
		_ = sink.Status(terminal.status, true)
	case status != nil:
		_ = sink.Status(status.status, true)
	}
}

func emitEvent(sink StreamSink, ev taskEvent) bool {
	if ev.kind == "status" {
		return sink.Status(ev.status, ev.final) == nil
	}
	return sink.Artifact(ev.artifact, ev.appendChunk, ev.lastChunk) == nil
}

type taskEvent struct {
	kind        string
	status      TaskStatus
	final       bool
	artifact    Artifact
	appendChunk bool
	lastChunk   bool
}

type brokerSink struct{ br *taskBroker }

func (k *brokerSink) OnTask(string, string) {}

func (k *brokerSink) Status(st TaskStatus, final bool) error {
	k.br.publish(taskEvent{kind: "status", status: st, final: final})
	return nil
}

func (k *brokerSink) Artifact(a Artifact, appendChunk, lastChunk bool) error {
	k.br.publish(taskEvent{kind: "artifact", artifact: a, appendChunk: appendChunk, lastChunk: lastChunk})
	return nil
}

type taskBroker struct {
	max    int
	mu     sync.Mutex
	buf    []taskEvent
	subs   map[int]*brokerSub
	nextID int
	done   bool
}

type brokerSub struct {
	ch       chan taskEvent
	overflow atomic.Bool
}

func newTaskBroker(max int) *taskBroker {
	if max <= 0 {
		max = defaultMaxBrokerEvents
	}
	return &taskBroker{max: max, subs: make(map[int]*brokerSub)}
}

func (b *taskBroker) publish(ev taskEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, ev)
	if len(b.buf) > b.max {
		b.buf = append([]taskEvent(nil), b.buf[len(b.buf)-b.max:]...)
	}
	for id, sub := range b.subs {
		select {
		case sub.ch <- ev:
		default:
			sub.overflow.Store(true)
			close(sub.ch)
			delete(b.subs, id)
		}
	}
}

func (b *taskBroker) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done {
		return
	}
	b.done = true
	for id, sub := range b.subs {
		close(sub.ch)
		delete(b.subs, id)
	}
}

func (b *taskBroker) subscribe() ([]taskEvent, <-chan taskEvent, *atomic.Bool, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	replay := append([]taskEvent(nil), b.buf...)
	sub := &brokerSub{ch: make(chan taskEvent, 64)}
	if b.done {
		close(sub.ch)
		return replay, sub.ch, &sub.overflow, func() {}
	}
	id := b.nextID
	b.nextID++
	b.subs[id] = sub
	return replay, sub.ch, &sub.overflow, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.subs[id]; ok {
			close(sub.ch)
			delete(b.subs, id)
		}
	}
}
