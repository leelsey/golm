// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/provider/anthropic"
	"github.com/leelsey/golm/provider/google"
	"github.com/leelsey/golm/provider/openai"
)

// "Off" and "don't ask" used to be the same request.
func TestDisabledThinkingIsSaidOutLoud(t *testing.T) {
	body := captureAnthropicRequest(t, golm.Request{
		Model: "claude-opus-5", MaxTokens: 64, Thinking: golm.ThinkingConfig{Mode: golm.ThinkingDisabled},
	})
	th, ok := body["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("no thinking field — omitting it runs adaptive on this model, which is the opposite request: %v", body)
	}
	if th["type"] != "disabled" {
		t.Errorf(`thinking.type = %v, want "disabled"`, th["type"])
	}

	body = captureAnthropicRequest(t, golm.Request{Model: "claude-opus-5", MaxTokens: 64})
	if _, ok := body["thinking"]; ok {
		t.Errorf("ThinkingOff put a thinking field on the wire: %v", body["thinking"])
	}
}

// Temperature is incompatible with thinking.
func TestDisablingThinkingReturnsTheTemperature(t *testing.T) {
	temp := 0.0
	for _, tc := range []struct {
		mode golm.ThinkingMode
		want bool
	}{
		{golm.ThinkingDisabled, true},
		{golm.ThinkingOff, true},
		{golm.ThinkingAuto, false},
		{golm.ThinkingBudget, false},
	} {
		body := captureAnthropicRequest(t, golm.Request{
			Model: "claude-sonnet-4-5", MaxTokens: 4096, Temperature: &temp,
			Thinking: golm.ThinkingConfig{Mode: tc.mode},
		})
		if _, ok := body["temperature"]; ok != tc.want {
			t.Errorf("mode %v: temperature present = %v, want %v", tc.mode, ok, tc.want)
		}
	}
}

// The other two adapters cannot express it.
func TestDisabledThinkingNeverEnablesItElsewhere(t *testing.T) {
	t.Run("openai", func(t *testing.T) {
		body := captureRequest(t, func(u string) {
			_, _ = openai.New("k").WithBaseURL(u).WithoutRetry().Complete(context.Background(), golm.Request{
				Model: "o3-mini", MaxTokens: 64, Thinking: golm.ThinkingConfig{Mode: golm.ThinkingDisabled},
			})
		}, `{"id":"c","choices":[{"index":0,"message":{"content":"x"},"finish_reason":"stop"}],"usage":{}}`)
		if v, ok := body["reasoning_effort"]; ok {
			t.Errorf("reasoning_effort = %v on a request that asked for no thinking", v)
		}
	})
	t.Run("google", func(t *testing.T) {
		body := captureRequest(t, func(u string) {
			_, _ = google.New("k").WithBaseURL(u).WithoutRetry().Complete(context.Background(), golm.Request{
				Model: "gemini-2.0-flash", MaxTokens: 64, Thinking: golm.ThinkingConfig{Mode: golm.ThinkingDisabled},
			})
		}, `{"candidates":[{"content":{"parts":[{"text":"x"}]},"finishReason":"STOP"}],"usageMetadata":{}}`)
		gc, _ := body["generationConfig"].(map[string]any)
		if gc == nil {
			return
		}
		if v, ok := gc["thinkingConfig"]; ok {
			t.Errorf("thinkingConfig = %v on a request that asked for no thinking", v)
		}
	})
}

func captureAnthropicRequest(t *testing.T, req golm.Request) map[string]any {
	t.Helper()
	return captureRequest(t, func(u string) {
		_, _ = anthropic.New("k").WithBaseURL(u).WithoutRetry().Complete(context.Background(), req)
	}, `{"id":"m","content":[{"type":"text","text":"x"}],"stop_reason":"end_turn","usage":{}}`)
}

func captureRequest(t *testing.T, call func(baseURL string), reply string) map[string]any {
	t.Helper()
	var out map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &out); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(reply))
	}))
	defer srv.Close()
	call(srv.URL)
	if out == nil {
		t.Fatal("no request reached the server")
	}
	return out
}
