// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/leelsey/golm/internal/jsonrpc"
)

const maxConcurrentRequests = 64

const maxQueuedNotifications = 256

const drainTimeout = 5 * time.Second

// Handler processes a JSON-RPC method call.
type Handler func(ctx context.Context, params json.RawMessage) (any, error)

// Server dispatches incoming JSON-RPC requests to registered handlers.
type Server struct {
	mu       sync.RWMutex
	handlers map[string]Handler
	drain    time.Duration
}

// NewServer returns an empty Server.
func NewServer() *Server { return &Server{handlers: make(map[string]Handler), drain: drainTimeout} }

// Handle registers h for method.
func (s *Server) Handle(method string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = h
}

// Serve reads requests from t until the transport closes.
func (s *Server) Serve(ctx context.Context, t Transport) error {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup

	cleanEOF := false
	defer func() {
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		if cleanEOF {
			select {
			case <-done:
			case <-time.After(s.drain):
			}
		}
		cancel()
		select {
		case <-done:
		case <-time.After(s.drain):
		}
	}()
	sem := make(chan struct{}, maxConcurrentRequests)
	notesem := make(chan struct{}, maxQueuedNotifications)
	notes := newNoteChain()
	for {
		msg, err := t.ReadMessage()
		if err != nil {
			if errors.Is(err, io.EOF) {
				cleanEOF = true
				return nil
			}
			if errors.Is(err, io.ErrClosedPipe) {
				return nil
			}
			return err
		}
		var req jsonrpc.Request
		if err := json.Unmarshal(msg, &req); err != nil {
			_ = s.write(t, jsonrpc.ErrorResponse(nil, jsonrpc.CodeParseError, "parse error"))
			continue
		}
		if req.JSONRPC != jsonrpc.Version {
			if !req.IsNotification() {
				_ = s.write(t, jsonrpc.ErrorResponse(req.ID, jsonrpc.CodeInvalidRequest, `invalid request: jsonrpc must be "2.0"`))
			}
			continue
		}

		notification := req.IsNotification()
		budget := sem
		if notification {
			budget = notesem
		}
		select {
		case budget <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		var wait <-chan struct{}
		var done chan struct{}
		if notification {
			wait, done = notes.next()
		}
		wg.Add(1)
		go func(req jsonrpc.Request) {
			defer wg.Done()
			defer func() { <-budget }()
			if notification {
				defer close(done)
				if !hold(ctx, wait) {
					return
				}
			}
			if resp := s.handle(ctx, &req); resp != nil {
				_ = s.write(t, resp)
			}
		}(req)
	}
}

func (s *Server) handle(ctx context.Context, req *jsonrpc.Request) *jsonrpc.Response {
	s.mu.RLock()
	h, ok := s.handlers[req.Method]
	s.mu.RUnlock()
	if !ok {
		if req.IsNotification() {
			return nil
		}
		return jsonrpc.ErrorResponse(req.ID, jsonrpc.CodeMethodNotFound, "method not found: "+req.Method)
	}
	result, err := h(ctx, req.Params)
	if req.IsNotification() {
		return nil
	}
	if err != nil {
		var je *jsonrpc.Error
		if errors.As(err, &je) {
			return &jsonrpc.Response{JSONRPC: jsonrpc.Version, ID: req.ID, Error: je}
		}
		return jsonrpc.ErrorResponse(req.ID, jsonrpc.CodeInternalError, err.Error())
	}
	raw, mErr := json.Marshal(result)
	if mErr != nil {
		return jsonrpc.ErrorResponse(req.ID, jsonrpc.CodeInternalError, mErr.Error())
	}
	return jsonrpc.ResultResponse(req.ID, raw)
}

func (s *Server) write(t Transport, resp *jsonrpc.Response) error {
	b, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	return t.WriteMessage(b)
}
