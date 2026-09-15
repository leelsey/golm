// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

func callWithEffort(t *testing.T, model string, effort golm.Effort) (body map[string]any, sawRequest bool, err error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"id":"m","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{}}`))
	}))
	defer srv.Close()

	c := New("k").WithBaseURL(srv.URL).WithoutRetry()
	_, err = c.Complete(context.Background(), golm.Request{
		Model:    model,
		Effort:   effort,
		Messages: []golm.Message{golm.UserText("hi")},
	})
	return body, sawRequest, err
}

func TestEffortIsGatedByModel(t *testing.T) {
	tests := []struct {
		name    string
		model   string
		effort  golm.Effort
		wantErr string
		wantSet bool
	}{
		{"current_model_full_ladder", "claude-opus-5", golm.EffortXHigh, "", true},
		{"current_model_max", "claude-sonnet-5", golm.EffortMax, "", true},
		{"fable_full_ladder", "claude-fable-5", golm.EffortMax, "", true},

		{"four_six_takes_high", "claude-opus-4-6", golm.EffortHigh, "", true},
		{"four_six_rejects_xhigh", "claude-opus-4-6", golm.EffortXHigh, "does not have effort", false},
		{"sonnet_four_six_rejects_xhigh", "claude-sonnet-4-6", golm.EffortXHigh, "does not have effort", false},

		{"four_five_takes_high", "claude-opus-4-5", golm.EffortHigh, "", true},
		{"four_five_rejects_max", "claude-opus-4-5", golm.EffortMax, "does not have effort", false},

		{"sonnet_four_five_rejects_effort", "claude-sonnet-4-5", golm.EffortLow, "does not accept an effort", false},
		{"haiku_four_five_rejects_effort", "claude-haiku-4-5", golm.EffortLow, "does not accept an effort", false},
		{"claude_three_rejects_effort", "claude-3-5-sonnet-20241022", golm.EffortLow, "does not accept an effort", false},

		{"no_effort_no_field", "claude-sonnet-4-5", golm.EffortNone, "", false},

		{"unknown_model_is_forwarded", "claude-opus-9", golm.EffortMax, "", true},

		{"bedrock_prefix_normalises", "anthropic.claude-sonnet-4-5", golm.EffortLow, "does not accept an effort", false},
		{"vertex_suffix_normalises", "claude-opus-4-5@20251101", golm.EffortMax, "does not have effort", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, sawRequest, err := callWithEffort(t, tt.model, tt.effort)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("model %q effort %q: want an error naming %q, got none", tt.model, tt.effort, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want it to mention %q", err, tt.wantErr)
				}

				if sawRequest {
					t.Error("the request was sent anyway; the gate must refuse before the call")
				}
				return
			}
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}
			oc, ok := body["output_config"].(map[string]any)
			if tt.wantSet {
				if !ok || oc["effort"] != string(tt.effort) {
					t.Fatalf("output_config.effort missing or wrong on the wire: %v", body["output_config"])
				}
				return
			}
			if ok {
				t.Fatalf("output_config must be absent, got %v", oc)
			}
		})
	}
}

func TestIsEffortModel(t *testing.T) {
	for model, want := range map[string]bool{
		"claude-opus-5":     true,
		"claude-fable-5":    true,
		"claude-opus-4-5":   true,
		"claude-sonnet-4-5": false,
		"claude-haiku-4-5":  false,
		"claude-3-opus":     false,
		"claude-opus-9":     true,
	} {
		if got := IsEffortModel(model); got != want {
			t.Errorf("IsEffortModel(%q) = %v, want %v", model, got, want)
		}
	}
}
