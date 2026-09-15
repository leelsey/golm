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

func TestMultimodalInputWireFormat(t *testing.T) {
	var body struct {
		Messages []struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	c := New("key").WithBaseURL(srv.URL)
	_, err := c.Complete(context.Background(), golm.Request{
		Model: "m",
		Messages: []golm.Message{{Role: golm.RoleUser, Content: []golm.Content{
			golm.Text{Text: "describe"},
			golm.Image{MediaType: "image/png", Data: []byte{1, 2, 3}},
			golm.Audio{MediaType: "audio/wav", Data: []byte{4, 5, 6}},
		}}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(body.Messages) != 1 {
		t.Fatalf("messages = %d", len(body.Messages))
	}
	types := map[string]bool{}
	for _, p := range body.Messages[0].Content {
		if s, ok := p["type"].(string); ok {
			types[s] = true
		}
	}
	if !types["text"] || !types["image_url"] || !types["input_audio"] {
		t.Errorf("multimodal parts missing: %v", types)
	}
}
