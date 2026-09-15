// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package rpc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/leelsey/golm/internal/jsonrpc"
)

func TestClientMatchesResponseWithMethodField(t *testing.T) {
	a, b := NewPipe()
	go func() {
		msg, err := a.ReadMessage()
		if err != nil {
			return
		}
		var req jsonrpc.Request
		_ = json.Unmarshal(msg, &req)
		_ = a.WriteMessage([]byte(`{"jsonrpc":"2.0","id":` + string(req.ID) + `,"result":{"ok":true},"method":"noise"}`))
	}()
	c := NewClient(b)
	defer c.Close()
	var out struct {
		OK bool `json:"ok"`
	}
	if err := c.Call(context.Background(), "ping", nil, &out); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !out.OK {
		t.Error("response carrying a method field was dropped")
	}
}
