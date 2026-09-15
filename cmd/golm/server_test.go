// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestRunHTTPServerShutsDownOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- runHTTPServer(ctx, "127.0.0.1:0", http.NewServeMux()) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected clean shutdown, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runHTTPServer did not shut down on ctx cancel")
	}
}

func TestRunHTTPServerListenError(t *testing.T) {
	if err := runHTTPServer(context.Background(), "127.0.0.1:-1", http.NewServeMux()); err == nil {
		t.Fatal("expected listen error for a bad address")
	}
}
