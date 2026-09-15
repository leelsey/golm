// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package google

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

func TestAssistantImageResentToGemini(t *testing.T) {
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`)
	}))
	defer srv.Close()

	c := New("k").WithBaseURL(srv.URL)
	_, _ = c.Complete(context.Background(), golm.Request{Model: "m", Messages: []golm.Message{
		{Role: golm.RoleAssistant, Content: []golm.Content{golm.Image{MediaType: "image/png", Data: []byte{1, 2, 3}}}},
		golm.UserText("again"),
	}})
	if !strings.Contains(string(raw), "AQID") {
		t.Errorf("assistant image not re-sent to Gemini: %s", raw)
	}
}
