// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package google

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leelsey/golm"
)

func TestCompleteCachedTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":30,"candidatesTokenCount":5,"thoughtsTokenCount":2,"cachedContentTokenCount":12}}`)
	}))
	defer srv.Close()

	c := New("key").WithBaseURL(srv.URL)
	resp, err := c.Complete(context.Background(), golm.Request{Model: "gemini-x", Messages: []golm.Message{golm.UserText("hello")}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	want := golm.Usage{InputTokens: 30, OutputTokens: 5, ThinkingTokens: 2, CacheReadTokens: 12}
	if resp.Usage != want {
		t.Errorf("usage = %+v, want %+v", resp.Usage, want)
	}
}
