// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package netbus

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteEventNoFrameInjection(t *testing.T) {
	rec := httptest.NewRecorder()
	_ = writeEvent(rec, Event{ID: 1, Topic: "t", Kind: "x\n\ndata: {\"forged\":1}"})
	body := rec.Body.String()
	if strings.Contains(body, "\n\ndata:") {
		t.Errorf("SSE frame injection via Kind: %q", body)
	}
}

func TestPublishBodyCap(t *testing.T) {
	h := NewHub()
	h.MaxPublishBytes = 64
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	body := `{"topic":"t","data":"` + strings.Repeat("a", 200) + `"}`
	resp, err := http.Post(srv.URL+"/publish", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusAccepted {
		t.Fatalf("oversized publish accepted (status %d)", resp.StatusCode)
	}
}
