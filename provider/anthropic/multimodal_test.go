// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leelsey/golm"
)

func TestImageInputWireFormat(t *testing.T) {
	var body struct {
		Messages []struct {
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer srv.Close()

	c := New("key").WithBaseURL(srv.URL)
	_, err := c.Complete(context.Background(), golm.Request{
		Model: "m",
		Messages: []golm.Message{{Role: golm.RoleUser, Content: []golm.Content{
			golm.Text{Text: "describe"},
			golm.Image{MediaType: "image/png", Data: []byte{1, 2, 3}},
		}}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	var img map[string]any
	for _, b := range body.Messages[0].Content {
		if b["type"] == "image" {
			img = b
		}
	}
	if img == nil {
		t.Fatalf("no image block in request: %+v", body.Messages[0].Content)
	}
	src, _ := img["source"].(map[string]any)
	if src["type"] != "base64" || src["media_type"] != "image/png" {
		t.Errorf("image source wrong: %+v", src)
	}
}
