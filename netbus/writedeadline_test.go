// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package netbus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func waitUntil(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
	return true
}

func TestStalledSubscriberDroppedByWriteDeadline(t *testing.T) {
	hub := NewHub()
	hub.WriteTimeout = 100 * time.Millisecond
	srv := httptest.NewServer(hub.Handler())
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := fmt.Fprint(conn, "GET /subscribe HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
		t.Fatalf("request: %v", err)
	}

	if !waitUntil(2*time.Second, func() bool { return hub.SubscriberCount() == 1 }) {
		t.Fatal("subscriber never registered")
	}

	blob := bytes.Repeat([]byte{'A'}, 1<<20)
	data, _ := json.Marshal(string(blob))
	body, _ := json.Marshal(Event{Topic: "t", Data: data})
	for i := 0; i < 4; i++ {
		resp, err := http.Post(srv.URL+"/publish", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("publish: %v", err)
		}
		resp.Body.Close()
	}

	if !waitUntil(3*time.Second, func() bool { return hub.SubscriberCount() == 0 }) {
		t.Fatal("stalled subscriber not dropped; write deadline not enforced")
	}
}
