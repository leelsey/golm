// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package netbus

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSubscribeAfterCloseAllReturns(t *testing.T) {
	h := NewHub()
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	h.CloseAll()

	done := make(chan error, 1)
	go func() {
		resp, err := http.Get(srv.URL + "/subscribe")
		if err != nil {
			done <- err
			return
		}
		_, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("post-shutdown subscriber parked instead of returning")
	}
}
