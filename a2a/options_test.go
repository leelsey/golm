// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package a2a

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TaskStore is the documented way to tune retention before serving.
func TestServerTaskStoreIsTheOneInUse(t *testing.T) {
	s := NewServer(AgentCard{Name: "t"}, echoHandler{})
	st := s.TaskStore()
	if st == nil {
		t.Fatal("TaskStore() is nil; retention cannot be tuned")
	}
	st.MaxTasks, st.MaxBrokerEvents = 11, 22
	if got := s.TaskStore(); got.MaxTasks != 11 || got.MaxBrokerEvents != 22 {
		t.Errorf("tuning did not stick: MaxTasks=%d MaxBrokerEvents=%d", got.MaxTasks, got.MaxBrokerEvents)
	}
}

type countingTransport struct {
	n    atomic.Int32
	next http.RoundTripper
}

func (t *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.n.Add(1)
	return t.next.RoundTrip(r)
}

// WithHTTPClient exists so a caller can supply timeouts, proxies or a instrumented transport.
func TestClientWithHTTPClientIsTheOneUsed(t *testing.T) {
	srv := httptest.NewServer(NewServer(AgentCard{Name: "t"}, echoHandler{}).HTTPHandler())
	defer srv.Close()

	tr := &countingTransport{next: http.DefaultTransport}
	c := NewClient(srv.URL).WithHTTPClient(&http.Client{Transport: tr})
	if _, err := c.SendMessage(t.Context(), "hi"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if got := tr.n.Load(); got == 0 {
		t.Error("the supplied client made no request; it was stored but not used")
	}
}
