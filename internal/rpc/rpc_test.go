// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/leelsey/golm/internal/jsonrpc"
)

func TestClientServerRoundTrip(t *testing.T) {
	cEnd, sEnd := NewPipe()
	srv := NewServer()
	srv.Handle("echo", func(_ context.Context, params json.RawMessage) (any, error) {
		var in struct {
			Msg string `json:"msg"`
		}
		_ = json.Unmarshal(params, &in)
		return map[string]string{"echo": in.Msg}, nil
	})
	srv.Handle("boom", func(_ context.Context, _ json.RawMessage) (any, error) {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "bad params")
	})
	var pinged sync.WaitGroup
	pinged.Add(1)
	srv.Handle("ping", func(_ context.Context, _ json.RawMessage) (any, error) {
		pinged.Done()
		return nil, nil
	})

	go func() { _ = srv.Serve(context.Background(), sEnd) }()

	client := NewClient(cEnd)
	defer client.Close()

	var out struct {
		Echo string `json:"echo"`
	}
	if err := client.Call(context.Background(), "echo", map[string]string{"msg": "hi"}, &out); err != nil {
		t.Fatalf("echo: %v", err)
	}
	if out.Echo != "hi" {
		t.Errorf("echo = %q, want hi", out.Echo)
	}

	var je *jsonrpc.Error
	if err := client.Call(context.Background(), "boom", nil, nil); !errors.As(err, &je) || je.Code != jsonrpc.CodeInvalidParams {
		t.Errorf("boom: expected invalid-params error, got %v", err)
	}
	if err := client.Call(context.Background(), "missing", nil, nil); !errors.As(err, &je) || je.Code != jsonrpc.CodeMethodNotFound {
		t.Errorf("missing: expected method-not-found, got %v", err)
	}

	if err := client.Notify(context.Background(), "ping", nil); err != nil {
		t.Fatalf("notify: %v", err)
	}
	pinged.Wait()
}
