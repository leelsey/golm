// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package google

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leelsey/golm"
)

func TestMultimodalOutputParsing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"here"},{"inlineData":{"mimeType":"image/png","data":"AQID"}}]},"finishReason":"STOP"}]}`)
	}))
	defer srv.Close()

	resp, err := New("key").WithBaseURL(srv.URL).Complete(context.Background(),
		golm.Request{Model: "x", Messages: []golm.Message{golm.UserText("draw")}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	var img *golm.Image
	for _, c := range resp.Message.Content {
		if im, ok := c.(golm.Image); ok {
			img = &im
		}
	}
	if img == nil {
		t.Fatalf("no image in response: %+v", resp.Message.Content)
	}
	if img.MediaType != "image/png" || !bytes.Equal(img.Data, []byte{1, 2, 3}) {
		t.Errorf("image output wrong: mt=%q data=%v", img.MediaType, img.Data)
	}
}
