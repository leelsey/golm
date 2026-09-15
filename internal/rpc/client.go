// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/leelsey/golm/internal/jsonrpc"
)

// ErrClosed is returned when a call is made on a closed client.
var ErrClosed = errors.New("rpc: client closed")

// Client is a JSON-RPC client over a Transport.
type Client struct {
	t       Transport
	nextID  int64
	mu      sync.Mutex
	pending map[string]chan *jsonrpc.Response
	closed  bool
	readErr error
}

// NewClient starts a client over t.
func NewClient(t Transport) *Client {
	c := &Client{t: t, pending: make(map[string]chan *jsonrpc.Response)}
	go c.readLoop()
	return c
}

func (c *Client) readLoop() {
	for {
		msg, err := c.t.ReadMessage()
		if err != nil {
			c.mu.Lock()
			c.closed = true
			c.readErr = err
			for k, ch := range c.pending {
				close(ch)
				delete(c.pending, k)
			}
			c.mu.Unlock()
			return
		}

		var probe struct {
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(msg, &probe) == nil && probe.Method != "" && len(probe.Result) == 0 && len(probe.Error) == 0 {
			continue
		}
		var resp jsonrpc.Response
		if json.Unmarshal(msg, &resp) != nil {
			continue
		}
		key := string(resp.ID)
		c.mu.Lock()
		ch := c.pending[key]
		delete(c.pending, key)
		c.mu.Unlock()
		if ch != nil {
			ch <- &resp
		}
	}
}

func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	if raw, ok := params.(json.RawMessage); ok {
		return raw, nil
	}
	return json.Marshal(params)
}

// Call invokes method with params and unmarshals the result into result.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	p, err := marshalParams(params)
	if err != nil {
		return err
	}
	n := atomic.AddInt64(&c.nextID, 1)
	key := strconv.FormatInt(n, 10)
	req := jsonrpc.Request{JSONRPC: jsonrpc.Version, ID: json.RawMessage(key), Method: method, Params: p}
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}

	ch := make(chan *jsonrpc.Response, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.pending[key] = ch
	c.mu.Unlock()

	werr := make(chan error, 1)
	go func() { werr <- c.t.WriteMessage(b) }()
	select {
	case err := <-werr:
		if err != nil {
			c.mu.Lock()
			delete(c.pending, key)
			c.mu.Unlock()
			return err
		}
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
		return ctx.Err()
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			return c.readErr
		}
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, result)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
		return ctx.Err()
	}
}

// Notify sends a notification.
func (c *Client) Notify(ctx context.Context, method string, params any) error {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrClosed
	}
	p, err := marshalParams(params)
	if err != nil {
		return err
	}
	req := jsonrpc.Request{JSONRPC: jsonrpc.Version, Method: method, Params: p}
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	werr := make(chan error, 1)
	go func() { werr <- c.t.WriteMessage(b) }()
	select {
	case err := <-werr:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close closes the underlying transport.
func (c *Client) Close() error { return c.t.Close() }

// Live reports whether the read loop is still running.
func (c *Client) Live() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed
}

// Err is why the read loop ended, or nil while it is still running.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readErr
}
