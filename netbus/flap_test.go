// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package netbus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leelsey/golm"
)

func TestReceiveBacksOffOnFlap(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("content-type", "text/event-stream")
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}))
	defer srv.Close()

	bus := golm.NewBus()
	defer bus.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 900*time.Millisecond)
	defer cancel()
	NewClient(srv.URL).receive(ctx, bus, nil)

	if n := atomic.LoadInt32(&calls); n > 7 {
		t.Errorf("instant-drop flapping caused %d reconnects in ~0.9s; backoff not applied", n)
	}
}
