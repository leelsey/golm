// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package a2a

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestClientCapsResponseBody.
func TestClientCapsResponseBody(t *testing.T) {
	old := maxResponseBytes
	maxResponseBytes = 32
	defer func() { maxResponseBytes = old }()

	big := `{"name":"` + strings.Repeat("a", 4096) + `"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, big)
	}))
	defer srv.Close()

	if _, err := NewClient(srv.URL).AgentCard(context.Background()); err == nil {
		t.Fatal("expected a decode error from the capped body, got nil")
	}
}
