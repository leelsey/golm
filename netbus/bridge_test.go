// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package netbus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leelsey/golm"
)

func TestBridgeStopsReceiveOnBusClose(t *testing.T) {
	var subs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "subscribe") {
			atomic.AddInt32(&subs, 1)
		}
		w.Header().Set("content-type", "text/event-stream")
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}))
	defer srv.Close()

	bus := golm.NewBus()
	done := make(chan error, 1)
	go func() { done <- NewClient(srv.URL).Bridge(context.Background(), bus) }()
	time.Sleep(200 * time.Millisecond)

	bus.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Bridge did not return after bus.Close")
	}

	time.Sleep(200 * time.Millisecond)
	a := atomic.LoadInt32(&subs)
	time.Sleep(400 * time.Millisecond)
	if b := atomic.LoadInt32(&subs); b != a {
		t.Errorf("receive goroutine still reconnecting after bus.Close (%d new subscribes)", b-a)
	}
}
