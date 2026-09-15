// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package a2a

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/leelsey/golm/internal/jsonrpc"
)

func TestTaskNotFoundCode(t *testing.T) {
	hs := httptest.NewServer(NewServer(CardForAgent("p", "p", "http://x/"), plainHandler{}).HTTPHandler())
	defer hs.Close()
	err := NewClient(hs.URL).call(context.Background(), "tasks/get", TaskQueryParams{ID: "nope"}, nil)
	je, ok := err.(*jsonrpc.Error)
	if !ok || je.Code != codeTaskNotFound {
		t.Errorf("not-found error = %v, want code %d", err, codeTaskNotFound)
	}
}

func TestCancelTerminalCode(t *testing.T) {
	hs := httptest.NewServer(NewServer(CardForAgent("p", "p", "http://x/"), plainHandler{}).HTTPHandler())
	defer hs.Close()
	c := NewClient(hs.URL)
	var sent Task
	if err := c.call(context.Background(), "message/send", MessageSendParams{Message: userMessage("hi")}, &sent); err != nil {
		t.Fatalf("send: %v", err)
	}
	err := c.call(context.Background(), "tasks/cancel", TaskQueryParams{ID: sent.ID}, nil)
	je, ok := err.(*jsonrpc.Error)
	if !ok || je.Code != codeTaskNotCancelable {
		t.Errorf("cancel-terminal error = %v, want code %d", err, codeTaskNotCancelable)
	}
}
