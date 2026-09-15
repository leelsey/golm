// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/leelsey/golm/internal/jsonrpc"
)

// Peer is both ends of a JSON-RPC connection over ONE transport.
type Peer struct {
	t Transport

	hmu      sync.RWMutex
	handlers map[string]Handler

	mu      sync.Mutex
	nextID  int64
	pending map[string]chan *jsonrpc.Response
	closed  bool
	readErr error

	inflight chan struct{}

	queued chan struct{}
	wg     sync.WaitGroup

	drain time.Duration

	notes *noteChain
}

func (p *Peer) drainWait() time.Duration {
	if p.drain > 0 {
		return p.drain
	}
	return drainTimeout
}

// NewPeer returns a Peer over t.
func NewPeer(t Transport) *Peer {
	return &Peer{
		t: t, handlers: make(map[string]Handler),
		pending:  make(map[string]chan *jsonrpc.Response),
		inflight: make(chan struct{}, maxConcurrentRequests),
		queued:   make(chan struct{}, maxQueuedNotifications),
		notes:    newNoteChain(),
	}
}

// Handle registers h for method.
func (p *Peer) Handle(method string, h Handler) {
	p.hmu.Lock()
	defer p.hmu.Unlock()
	p.handlers[method] = h
}

// Serve runs the read loop until the transport ends.
func (p *Peer) Serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)

	defer func() {
		done := make(chan struct{})
		go func() { p.wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(p.drainWait()):
		}
	}()
	defer cancel()
	for {
		msg, err := p.t.ReadMessage()
		if err != nil {
			p.fail(err)
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		p.route(ctx, msg)
	}
}

func (p *Peer) route(ctx context.Context, msg []byte) {
	var probe struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.Unmarshal(msg, &probe); err != nil {
		p.write(jsonrpc.ErrorResponse(nil, jsonrpc.CodeParseError, "invalid JSON"))
		return
	}
	if probe.Method == "" {
		p.deliver(msg)
		return
	}
	var req jsonrpc.Request
	if err := json.Unmarshal(msg, &req); err != nil {
		p.write(jsonrpc.ErrorResponse(probe.ID, jsonrpc.CodeInvalidRequest, "invalid request"))
		return
	}
	p.dispatch(ctx, req)
}

func (p *Peer) deliver(msg []byte) {
	var resp jsonrpc.Response
	if err := json.Unmarshal(msg, &resp); err != nil {
		return
	}
	key := string(resp.ID)
	p.mu.Lock()
	ch := p.pending[key]
	delete(p.pending, key)
	p.mu.Unlock()
	if ch != nil {
		ch <- &resp
	}
}

func (p *Peer) dispatch(ctx context.Context, req jsonrpc.Request) {
	p.hmu.RLock()
	h := p.handlers[req.Method]
	p.hmu.RUnlock()
	notification := len(req.ID) == 0
	if h == nil {
		if !notification {
			p.write(jsonrpc.ErrorResponse(req.ID, jsonrpc.CodeMethodNotFound, "no such method: "+req.Method))
		}
		return
	}
	budget := p.inflight
	if notification {
		budget = p.queued
	}
	select {
	case budget <- struct{}{}:
	case <-ctx.Done():
		return
	}

	var wait <-chan struct{}
	var done chan struct{}
	if notification {
		wait, done = p.notes.next()
	}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() { <-budget }()
		if notification {
			defer close(done)
			if !hold(ctx, wait) {
				return
			}
		}

		defer func() {
			if r := recover(); r != nil && !notification {
				p.write(jsonrpc.ErrorResponse(req.ID, jsonrpc.CodeInternalError, "handler panicked"))
			}
		}()
		result, err := h(ctx, req.Params)
		if notification {
			return
		}
		if err != nil {
			var jerr *jsonrpc.Error
			if errors.As(err, &jerr) {
				p.write(&jsonrpc.Response{JSONRPC: jsonrpc.Version, ID: req.ID, Error: jerr})
				return
			}
			p.write(jsonrpc.ErrorResponse(req.ID, jsonrpc.CodeInternalError, err.Error()))
			return
		}
		raw, merr := json.Marshal(result)
		if merr != nil {
			p.write(jsonrpc.ErrorResponse(req.ID, jsonrpc.CodeInternalError, merr.Error()))
			return
		}
		p.write(&jsonrpc.Response{JSONRPC: jsonrpc.Version, ID: req.ID, Result: raw})
	}()
}

func (p *Peer) write(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	_ = p.t.WriteMessage(b)
}

func (p *Peer) fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed, p.readErr = true, err
	for k, ch := range p.pending {
		close(ch)
		delete(p.pending, k)
	}
}

// Call invokes method on the far side and unmarshals the result into result.
func (p *Peer) Call(ctx context.Context, method string, params, result any) error {
	raw, err := marshalParams(params)
	if err != nil {
		return err
	}
	key := strconv.FormatInt(atomic.AddInt64(&p.nextID, 1), 10)
	b, err := json.Marshal(jsonrpc.Request{
		JSONRPC: jsonrpc.Version, ID: json.RawMessage(key), Method: method, Params: raw,
	})
	if err != nil {
		return err
	}
	ch := make(chan *jsonrpc.Response, 1)
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrClosed
	}
	p.pending[key] = ch
	p.mu.Unlock()

	if err := p.t.WriteMessage(b); err != nil {
		p.mu.Lock()
		delete(p.pending, key)
		p.mu.Unlock()
		return err
	}
	select {
	case resp, ok := <-ch:
		if !ok {
			p.mu.Lock()
			rerr := p.readErr
			p.mu.Unlock()
			if rerr == nil {
				rerr = ErrClosed
			}
			return rerr
		}
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, result)
		}
		return nil
	case <-ctx.Done():
		p.mu.Lock()
		delete(p.pending, key)
		p.mu.Unlock()
		return ctx.Err()
	}
}

// Notify sends a notification, for which no reply is expected.
func (p *Peer) Notify(ctx context.Context, method string, params any) error {
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return ErrClosed
	}
	raw, err := marshalParams(params)
	if err != nil {
		return err
	}
	b, err := json.Marshal(jsonrpc.Request{JSONRPC: jsonrpc.Version, Method: method, Params: raw})
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.t.WriteMessage(b)
}

// Close ends the connection.
func (p *Peer) Close() error {
	p.fail(ErrClosed)
	return p.t.Close()
}
