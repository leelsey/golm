// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package google

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leelsey/golm"
)

func TestResponseModalityRequested(t *testing.T) {
	var body struct {
		GenerationConfig struct {
			ResponseModalities []string `json:"responseModalities"`
		} `json:"generationConfig"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`)
	}))
	defer srv.Close()

	_, err := New("k").WithBaseURL(srv.URL).Complete(context.Background(), golm.Request{
		Model:              "x",
		Messages:           []golm.Message{golm.UserText("draw")},
		ResponseModalities: []string{"image"},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	found := false
	for _, m := range body.GenerationConfig.ResponseModalities {
		if m == "IMAGE" {
			found = true
		}
	}
	if !found {
		t.Errorf("responseModalities not set: %v", body.GenerationConfig.ResponseModalities)
	}
}

func TestBadInlineDataSkipped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"hi"},{"inlineData":{"mimeType":"image/png","data":"@@not-base64@@"}}]},"finishReason":"STOP"}]}`)
	}))
	defer srv.Close()

	resp, err := New("k").WithBaseURL(srv.URL).Complete(context.Background(),
		golm.Request{Model: "x", Messages: []golm.Message{golm.UserText("draw")}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	for _, c := range resp.Message.Content {
		if _, ok := c.(golm.Image); ok {
			t.Error("undecodable inlineData should be skipped, not turned into an empty Image")
		}
	}
}
