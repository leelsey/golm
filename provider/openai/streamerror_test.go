// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

func TestStreamMidStreamErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"partial"}}]}`+"\n\n")
		fl.Flush()
		fmt.Fprint(w, `data: {"error":{"type":"server_error","message":"backend exploded"}}`+"\n\n")
		fl.Flush()
	}))
	defer srv.Close()

	c := New("k").WithBaseURL(srv.URL)
	_, err := c.Stream(context.Background(), golm.Request{Model: "gpt-4o", Messages: []golm.Message{golm.UserText("hi")}}, func(golm.StreamEvent) error { return nil })
	if err == nil {
		t.Fatal("mid-stream error frame returned nil error (silent truncation)")
	}
	if !strings.Contains(err.Error(), "backend exploded") {
		t.Errorf("err = %v, want it to carry the server's error message", err)
	}
}
