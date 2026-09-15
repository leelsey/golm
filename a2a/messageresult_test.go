// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package a2a

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leelsey/golm/internal/jsonrpc"
)

func TestSendMessageHandlesMessageResult(t *testing.T) {
	msg := Message{Role: "agent", Kind: "message", Parts: []Part{TextPart("direct answer")}}
	raw, _ := json.Marshal(msg)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(jsonrpc.ResultResponse(json.RawMessage("1"), raw))
	}))
	defer srv.Close()

	out, err := NewClient(srv.URL).SendMessage(context.Background(), "hi")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if out != "direct answer" {
		t.Fatalf("out = %q, want %q (Message result dropped)", out, "direct answer")
	}
}

func TestSendMessageHandlesUntaggedMessageResult(t *testing.T) {
	raw := []byte(`{"role":"agent","parts":[{"kind":"text","text":"untagged answer"}],"messageId":"m1"}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(jsonrpc.ResultResponse(json.RawMessage("1"), raw))
	}))
	defer srv.Close()

	out, err := NewClient(srv.URL).SendMessage(context.Background(), "hi")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if out != "untagged answer" {
		t.Fatalf("out = %q, want %q", out, "untagged answer")
	}
}

func TestSendMessageNoResultErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1}`))
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL).SendMessage(context.Background(), "hi")
	if err == nil || !strings.Contains(err.Error(), "no result") {
		t.Fatalf("err = %v, want a clear no-result error", err)
	}
}

func TestSendMessageStreamTerminalFrames(t *testing.T) {
	send := func(result string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("content-type", "text/event-stream")
			resp, _ := json.Marshal(jsonrpc.ResultResponse(json.RawMessage("1"), json.RawMessage(result)))
			_, _ = w.Write([]byte("data: " + string(resp) + "\n\n"))
		}))
	}

	msgSrv := send(`{"kind":"message","role":"agent","parts":[{"kind":"text","text":"final"}]}`)
	defer msgSrv.Close()
	out, err := NewClient(msgSrv.URL).SendMessageStream(context.Background(), "hi", nil)
	if err != nil || out != "final" {
		t.Fatalf("message frame: out=%q err=%v, want final/nil", out, err)
	}

	taskSrv := send(`{"kind":"task","id":"t1","status":{"state":"failed","message":{"role":"agent","parts":[{"kind":"text","text":"boom"}]}}}`)
	defer taskSrv.Close()
	out, err = NewClient(taskSrv.URL).SendMessageStream(context.Background(), "hi", nil)
	if err == nil {
		t.Fatalf("failed task frame surfaced as success: out=%q", out)
	}
}

func TestSendMessageStreamWorkingSnapshotNotFinal(t *testing.T) {
	frames := []string{
		`{"kind":"task","id":"t1","status":{"state":"working"},"artifacts":[{"parts":[{"kind":"text","text":"stale"}]}]}`,
		`{"kind":"artifact-update","taskId":"t1","artifact":{"parts":[{"kind":"text","text":"fresh"}]},"append":false}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		for _, f := range frames {
			resp, _ := json.Marshal(jsonrpc.ResultResponse(json.RawMessage("1"), json.RawMessage(f)))
			_, _ = w.Write([]byte("data: " + string(resp) + "\n\n"))
		}
	}))
	defer srv.Close()

	out, err := NewClient(srv.URL).SendMessageStream(context.Background(), "hi", nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if out != "fresh" {
		t.Fatalf("out = %q, want fresh (stale working snapshot outranked the deltas)", out)
	}
}

func TestSendMessageStreamInputRequiredSnapshot(t *testing.T) {
	frame := `{"kind":"task","id":"t1","status":{"state":"input-required","message":{"role":"agent","parts":[{"kind":"text","text":"which account?"}]}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		resp, _ := json.Marshal(jsonrpc.ResultResponse(json.RawMessage("1"), json.RawMessage(frame)))
		_, _ = w.Write([]byte("data: " + string(resp) + "\n\n"))
	}))
	defer srv.Close()

	out, err := NewClient(srv.URL).SendMessageStream(context.Background(), "hi", nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if out != "which account?" {
		t.Fatalf("out = %q, want the input-required question", out)
	}
}

func TestSendMessageStreamUnknownStateSnapshotNotFinal(t *testing.T) {
	frames := []string{
		`{"kind":"task","id":"t1","status":{"state":"unknown"},"artifacts":[{"parts":[{"kind":"text","text":"The answer is"}]}]}`,
		`{"kind":"artifact-update","taskId":"t1","artifact":{"parts":[{"kind":"text","text":"The answer is 42."}]},"append":false}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		for _, f := range frames {
			resp, _ := json.Marshal(jsonrpc.ResultResponse(json.RawMessage("1"), json.RawMessage(f)))
			_, _ = w.Write([]byte("data: " + string(resp) + "\n\n"))
		}
	}))
	defer srv.Close()

	out, err := NewClient(srv.URL).SendMessageStream(context.Background(), "hi", nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if out != "The answer is 42." {
		t.Fatalf("out = %q, want the full delta text (unknown-state snapshot outranked deltas)", out)
	}
}

func TestSendMessageStreamMessageChunksAppend(t *testing.T) {
	frames := []string{
		`{"kind":"message","role":"agent","parts":[{"kind":"text","text":"hel"}]}`,
		`{"kind":"message","role":"agent","parts":[{"kind":"text","text":"lo"}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		for _, f := range frames {
			resp, _ := json.Marshal(jsonrpc.ResultResponse(json.RawMessage("1"), json.RawMessage(f)))
			_, _ = w.Write([]byte("data: " + string(resp) + "\n\n"))
		}
	}))
	defer srv.Close()

	var deltas int
	out, err := NewClient(srv.URL).SendMessageStream(context.Background(), "hi", func(string) { deltas++ })
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if out != "hello" {
		t.Fatalf("out = %q, want hello (message chunks not appended)", out)
	}
	if deltas != 2 {
		t.Errorf("onDelta fired %d times, want 2", deltas)
	}
}

func TestCallSurfacesHTTPStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/html")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("<html>login required</html>"))
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL).SendMessage(context.Background(), "hi")
	if err == nil {
		t.Fatal("HTTP 401 surfaced as success")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v, want the HTTP status surfaced", err)
	}
}
