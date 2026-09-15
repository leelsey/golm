// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

func captureBody(t *testing.T, req golm.Request) string {
	t.Helper()
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{}}`)
	}))
	defer srv.Close()

	c := New("key").WithBaseURL(srv.URL)
	if _, err := c.Complete(context.Background(), req); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	return body
}

func f64(v float64) *float64 { return &v }

func TestExplicitZeroTemperatureForwarded(t *testing.T) {
	body := captureBody(t, golm.Request{
		Model:       "gpt-4o",
		Messages:    []golm.Message{golm.UserText("hi")},
		Temperature: f64(0),
	})
	if !strings.Contains(body, `"temperature":0`) {
		t.Errorf("explicit zero temperature not forwarded: %s", body)
	}
}

func TestBuildRequestReasoningModelGating(t *testing.T) {
	body := captureBody(t, golm.Request{
		Model:       "o3-mini",
		Messages:    []golm.Message{golm.UserText("hi")},
		Thinking:    golm.ThinkingConfig{Mode: golm.ThinkingBudget, Budget: 1024},
		Temperature: f64(0.5),
	})
	if !strings.Contains(body, "reasoning_effort") {
		t.Errorf("reasoning model body missing reasoning_effort: %s", body)
	}
	if strings.Contains(body, "temperature") {
		t.Errorf("reasoning model body must not contain temperature: %s", body)
	}
}

func TestBuildRequestChatModelGating(t *testing.T) {
	body := captureBody(t, golm.Request{
		Model:       "gpt-4o",
		Messages:    []golm.Message{golm.UserText("hi")},
		Thinking:    golm.ThinkingConfig{Mode: golm.ThinkingAuto},
		Temperature: f64(0.5),
	})
	if !strings.Contains(body, "temperature") {
		t.Errorf("chat model body missing temperature: %s", body)
	}
	if strings.Contains(body, "reasoning_effort") {
		t.Errorf("chat model body must not contain reasoning_effort: %s", body)
	}
}
