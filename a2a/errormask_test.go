// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leelsey/golm/internal/jsonrpc"
)

func TestSendMessageFailedTaskErrors(t *testing.T) {
	card := CardForAgent("fail-agent", "always fails", "http://example/")
	hs := httptest.NewServer(NewServer(card, failHandler{}).HTTPHandler())
	defer hs.Close()

	out, err := NewClient(hs.URL).SendMessage(context.Background(), "go")
	if err == nil {
		t.Fatalf("failed task surfaced as success: out=%q", out)
	}
}

func TestSendMessageStreamHTTPErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(jsonrpc.ErrorResponse(json.RawMessage("1"), jsonrpc.CodeInternalError, "boom"))
	}))
	defer srv.Close()

	out, err := NewClient(srv.URL).SendMessageStream(context.Background(), "go", nil)
	if err == nil {
		t.Fatalf("HTTP 500/JSON-RPC error surfaced as success: out=%q", out)
	}
}

func TestSendMessageStreamCanceledErrors(t *testing.T) {
	su := TaskStatusUpdateEvent{Kind: "status-update", Final: true, Status: TaskStatus{
		State: StateCanceled, Message: &Message{Role: "agent", Parts: []Part{TextPart("aborted")}}}}
	raw, _ := json.Marshal(su)
	frame, _ := json.Marshal(jsonrpc.ResultResponse(json.RawMessage("1"), raw))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", frame)
	}))
	defer srv.Close()

	out, err := NewClient(srv.URL).SendMessageStream(context.Background(), "go", nil)
	if err == nil {
		t.Fatalf("canceled task surfaced as success: out=%q", out)
	}
}
