// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package netbus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHubAuthToken(t *testing.T) {
	h := NewHub()
	h.AuthToken = "s3cret"
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/publish", "application/json", strings.NewReader(`{"topic":"t"}`))
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("tokenless publish status = %d, want 401", resp.StatusCode)
	}

	if err := NewClient(srv.URL).WithToken("s3cret").Publish(context.Background(), Event{Topic: "t", Kind: "k"}); err != nil {
		t.Fatalf("authorised publish: %v", err)
	}
	if got := len(h.ring.after(0)); got != 1 {
		t.Fatalf("ring has %d events, want 1", got)
	}

	sub, err := http.Get(srv.URL + "/subscribe")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	sub.Body.Close()
	if sub.StatusCode != http.StatusUnauthorized {
		t.Fatalf("tokenless subscribe status = %d, want 401", sub.StatusCode)
	}
}
