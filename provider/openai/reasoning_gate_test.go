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

// Two parameters hang off one question.
func TestReasoningGateFollowsTheModel(t *testing.T) {
	zero := 0.0
	for _, tc := range []struct {
		model    string
		wantTemp bool
		wantEff  string
	}{
		{"o3-mini", false, "high"},
		{"o1", false, "high"},
		{"gpt-5", false, "high"},
		{"gpt-5-mini", false, "high"},

		{"gpt-5-chat-latest", true, ""},
		{"gpt-4o", true, ""},
		{"llama3.1", true, ""},
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
				Model:       tc.model,
				MaxTokens:   64,
				Temperature: &zero,
				Effort:      golm.EffortHigh,
				Messages:    []golm.Message{golm.UserText("hi")},
			}); err != nil {
				t.Fatalf("Complete: %v", err)
			}

			if _, ok := sent["temperature"]; ok != tc.wantTemp {
				t.Errorf("temperature present = %v, want %v", ok, tc.wantTemp)
			}
			got, _ := sent["reasoning_effort"].(string)
			if got != tc.wantEff {
				t.Errorf("reasoning_effort = %q, want %q", got, tc.wantEff)
			}
		})
	}
}
