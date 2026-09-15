// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package openaiapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/openaiapi"
)

type fuzzRunner struct{}

func (fuzzRunner) StreamMessage(_ context.Context, s *golm.Session, msg golm.Message,
	fn func(golm.StreamEvent) error) (golm.Result, error) {
	_ = fn(golm.StreamEvent{Type: golm.EventTextDelta, Text: "ok"})
	return golm.Result{Message: golm.AssistantText("ok"), StopReason: golm.StopEndTurn}, nil
}

// This endpoint is served over HTTP to arbitrary OpenAI clients.
func FuzzChatCompletions(f *testing.F) {
	f.Add(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	f.Add(`{"model":"m","messages":[],"stream":true}`)
	f.Add(`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	f.Add(`{"model":"m","messages":null}`)
	f.Add(`{"model":"m","messages":[{"role":"user","content":null}]}`)
	f.Add(`{"model":"m","messages":[{"role":"user","content":"hi"}],"n":2}`)
	f.Add(`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{}]}`)
	f.Add(`{"model":"unknown","messages":[{"role":"user","content":"hi"}]}`)
	f.Add(`{"model":"m","messages":[{"role":"user","content":"hi"}],"golm_session":"../escape"}`)
	f.Add(`{}`)
	f.Add(`not json`)
	f.Add(``)

	srv := openaiapi.NewServer()
	srv.Add("m", fuzzRunner{}, "a test model")
	h := srv.Handler()

	f.Fuzz(func(t *testing.T, body string) {
		for _, path := range []string{"/v1/chat/completions", "/chat/completions"} {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			req.Header.Set("content-type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			res := rec.Result()
			if res.StatusCode < 200 || res.StatusCode > 599 {
				t.Fatalf("status %d for %q", res.StatusCode, body)
			}
			out := rec.Body.String()

			if strings.Contains(res.Header.Get("content-type"), "event-stream") &&
				!strings.Contains(out, "[DONE]") {
				t.Fatalf("a stream ended without [DONE]; the client hangs. body=%q", body)
			}

			if res.StatusCode >= 400 && strings.TrimSpace(out) == "" {
				t.Fatalf("status %d with an empty body for %q", res.StatusCode, body)
			}
		}
	})
}
