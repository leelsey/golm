// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package netbus

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublishStampsEmptyOrigin(t *testing.T) {
	h := NewHub()
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/publish", "application/json", strings.NewReader(`{"topic":"t","kind":"k"}`))
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("publish status = %d", resp.StatusCode)
	}

	evs := h.ring.after(0)
	if len(evs) != 1 {
		t.Fatalf("ring has %d events, want 1", len(evs))
	}
	if evs[0].Origin == "" {
		t.Fatal("origin-less event stored without a stamped origin")
	}
}
