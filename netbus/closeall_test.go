// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package netbus

import (
	"fmt"
	"net"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCloseAllDropsSubscribers(t *testing.T) {
	hub := NewHub()
	srv := httptest.NewServer(hub.Handler())
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := fmt.Fprint(conn, "GET /subscribe HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	if !waitUntil(2*time.Second, func() bool { return hub.SubscriberCount() == 1 }) {
		t.Fatal("subscriber never registered")
	}

	hub.CloseAll()
	if !waitUntil(2*time.Second, func() bool { return hub.SubscriberCount() == 0 }) {
		t.Error("CloseAll did not drop the subscriber")
	}
}
