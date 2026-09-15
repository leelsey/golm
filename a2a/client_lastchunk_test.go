// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package a2a

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStreamPrefersLastChunkOverPartialDeltas(t *testing.T) {
	frames := []string{
		`{"jsonrpc":"2.0","id":1,"result":{"kind":"artifact-update","taskId":"t1","artifact":{"artifactId":"a1","parts":[{"kind":"text","text":"par"}]},"append":false}}`,
		`{"jsonrpc":"2.0","id":1,"result":{"kind":"artifact-update","taskId":"t1","artifact":{"artifactId":"a1","parts":[{"kind":"text","text":"partial answer"}]},"lastChunk":true}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
			fl.Flush()
		}
	}))
	defer srv.Close()

	out, err := NewClient(srv.URL).SendMessageStream(context.Background(), "hi", nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if out != "partial answer" {
		t.Errorf("assembled = %q, want %q (LastChunk full text must win over partial deltas)", out, "partial answer")
	}
}
