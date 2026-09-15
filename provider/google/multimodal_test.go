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

func TestMultimodalInputWireFormat(t *testing.T) {
	var body struct {
		Contents []struct {
			Parts []map[string]any `json:"parts"`
		} `json:"contents"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`)
	}))
	defer srv.Close()

	c := New("key").WithBaseURL(srv.URL)
	_, err := c.Complete(context.Background(), golm.Request{
		Model: "gemini-x",
		Messages: []golm.Message{{Role: golm.RoleUser, Content: []golm.Content{
			golm.Text{Text: "describe"},
			golm.Image{MediaType: "image/png", Data: []byte{1, 2, 3}},
			golm.Audio{MediaType: "audio/wav", Data: []byte{4, 5, 6}},
		}}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(body.Contents) != 1 {
		t.Fatalf("contents = %d", len(body.Contents))
	}
	mimes := map[string]bool{}
	for _, p := range body.Contents[0].Parts {
		if inline, ok := p["inlineData"].(map[string]any); ok {
			if mt, ok := inline["mimeType"].(string); ok {
				mimes[mt] = true
			}
		}
	}
	if !mimes["image/png"] || !mimes["audio/wav"] {
		t.Errorf("inlineData mime types missing: %v", mimes)
	}
}
