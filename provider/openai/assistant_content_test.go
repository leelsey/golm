// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

// TestAssistantPlanCarried.
func TestAssistantPlanCarried(t *testing.T) {
	body := captureBody(t, golm.Request{
		Model: "gpt-4o",
		Messages: []golm.Message{
			{Role: golm.RoleAssistant, Content: []golm.Content{golm.Plan{Steps: []string{"one", "two"}}}},
		},
	})
	if !strings.Contains(body, "one\\ntwo") {
		t.Errorf("assistant plan not carried into content: %s", body)
	}
}

// TestAssistantUnsupportedContentErrors.
func TestAssistantUnsupportedContentErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("provider was called; the request should have failed before the wire")
	}))
	defer srv.Close()

	c := New("key").WithBaseURL(srv.URL)
	_, err := c.Complete(context.Background(), golm.Request{
		Model: "gpt-4o",
		Messages: []golm.Message{
			{Role: golm.RoleAssistant, Content: []golm.Content{golm.Image{MediaType: "image/png", Data: []byte{1}}}},
		},
	})
	if err == nil {
		t.Fatal("expected an error for unsupported assistant content, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported content") {
		t.Errorf("error = %v, want it to name the unsupported content", err)
	}
}
