// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package netbus

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestConcurrentPublishDeliversInIDOrder(t *testing.T) {
	h := NewHub()
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	_, ch := h.addSub(nil)
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"topic":"t","kind":"k%d","origin":"o"}`, i)
			resp, err := http.Post(srv.URL+"/publish", "application/json", bytes.NewReader([]byte(body)))
			if err != nil {
				t.Error(err)
				return
			}
			resp.Body.Close()
		}(i)
	}
	wg.Wait()

	var last int64
	for i := 0; i < n; i++ {
		ev := <-ch
		if ev.ID <= last {
			t.Fatalf("event %d arrived with ID %d after ID %d (out of order)", i, ev.ID, last)
		}
		last = ev.ID
	}
}
