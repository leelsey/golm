// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package openai

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leelsey/golm"
)

func TestAudioOutputParsing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"","audio":{"data":"BAUG","transcript":"hello"}},"finish_reason":"stop"}],"usage":{}}`)
	}))
	defer srv.Close()

	resp, err := New("key").WithBaseURL(srv.URL).Complete(context.Background(),
		golm.Request{Model: "m", Messages: []golm.Message{golm.UserText("speak")}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	var aud *golm.Audio
	var transcript string
	for _, c := range resp.Message.Content {
		switch v := c.(type) {
		case golm.Audio:
			a := v
			aud = &a
		case golm.Text:
			transcript = v.Text
		}
	}
	if aud == nil || !bytes.Equal(aud.Data, []byte{4, 5, 6}) {
		t.Fatalf("audio output missing/wrong: %+v", resp.Message.Content)
	}
	if transcript != "hello" {
		t.Errorf("transcript = %q, want hello", transcript)
	}
}
