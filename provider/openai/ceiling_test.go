// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leelsey/golm"
)

// The output ceiling has two spellings on this wire and neither is universal.
func TestCeilingSpellingFollowsTheModelFamily(t *testing.T) {
	for _, tc := range []struct {
		model, want, absent string
	}{
		{"o1", "max_completion_tokens", "max_tokens"},
		{"o3-mini", "max_completion_tokens", "max_tokens"},
		{"o4-mini", "max_completion_tokens", "max_tokens"},
		{"gpt-4o", "max_tokens", "max_completion_tokens"},

		{"gpt-5", "max_completion_tokens", "max_tokens"},
		{"gpt-5-mini", "max_completion_tokens", "max_tokens"},
		{"llama3.1", "max_tokens", "max_completion_tokens"},
		{"gpt-oss-120b", "max_tokens", "max_completion_tokens"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			var sent map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(b, &sent)
				w.Header().Set("content-type", "application/json")
				io.WriteString(w, `{"choices":[{"message":{"content":"hi"},"finish_reason":"stop"}],`+
					`"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
			}))
			t.Cleanup(srv.Close)

			c := New("key").WithBaseURL(srv.URL)
			if _, err := c.Complete(context.Background(), golm.Request{
				Model:     tc.model,
				MaxTokens: 512,
				Messages:  []golm.Message{golm.UserText("hi")},
			}); err != nil {
				t.Fatalf("Complete: %v", err)
			}

			if got, ok := sent[tc.want]; !ok {
				t.Errorf("%s missing — the ceiling never reached the wire: %v", tc.want, sent)
			} else if n, _ := got.(float64); int(n) != 512 {
				t.Errorf("%s = %v, want 512", tc.want, got)
			}
			if _, ok := sent[tc.absent]; ok {
				t.Errorf("%s went out as well; this model or server does not take it", tc.absent)
			}
		})
	}
}
