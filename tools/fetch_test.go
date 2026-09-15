// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package tools

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/leelsey/golm"
)

func fetchOnce(t *testing.T, f *Fetcher, url string) (string, error) {
	t.Helper()
	b, _ := json.Marshal(fetchArgs{URL: url})
	out, err := f.Tool().Execute(context.Background(), b)
	if err != nil {
		return "", err
	}
	return (golm.ToolResult{Content: out}).Text(), nil
}

func TestFetchRefusesPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "internal secrets")
	}))
	defer srv.Close()

	f := &Fetcher{}
	_, err := fetchOnce(t, f, srv.URL)
	if err == nil {
		t.Fatal("fetching loopback should be refused by default")
	}
	if !strings.Contains(err.Error(), "loopback") {
		t.Errorf("err = %v, want it to name the reason", err)
	}

	f = &Fetcher{AllowPrivate: true}
	got, err := fetchOnce(t, f, srv.URL)
	if err != nil {
		t.Fatalf("with AllowPrivate: %v", err)
	}
	if got != "internal secrets" {
		t.Errorf("body = %q", got)
	}
}

func TestFetchRefusesTheMetadataAddress(t *testing.T) {
	if why := blockedIP(net.ParseIP("169.254.169.254"), false); why == "" {
		t.Error("the cloud metadata address must be blocked")
	}
	cases := map[string]bool{
		"127.0.0.1":       true,
		"10.1.2.3":        true,
		"192.168.0.1":     true,
		"172.16.0.1":      true,
		"169.254.169.254": true,
		"0.0.0.0":         true,
		"224.0.0.1":       true,
		"::1":             true,
		"fe80::1":         true,
		"fd00::1":         true,
		"93.184.216.34":   false,
		"2606:2800::1":    false,
	}
	for addr, want := range cases {
		got := blockedIP(net.ParseIP(addr), false) != ""
		if got != want {
			t.Errorf("blockedIP(%s) blocked = %v, want %v", addr, got, want)
		}
	}

	if blockedIP(net.ParseIP("10.0.0.1"), true) != "" {
		t.Error("AllowPrivate should permit a private address")
	}
	if blockedIP(net.ParseIP("224.0.0.1"), true) == "" {
		t.Error("multicast is never a useful fetch")
	}
	if blockedIP(nil, true) == "" {
		t.Error("an unparseable address must not be dialled")
	}
}

func TestFetchRefusesOtherSchemes(t *testing.T) {
	f := &Fetcher{AllowPrivate: true}
	for _, u := range []string{"file:///etc/passwd", "gopher://x/1", "ftp://x/y", "not a url at all"} {
		if _, err := fetchOnce(t, f, u); err == nil {
			t.Errorf("fetching %q should be refused", u)
		}
	}
}

func TestFetchBoundsTheBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.Repeat("x", 4096))
	}))
	defer srv.Close()
	f := &Fetcher{AllowPrivate: true, MaxBytes: 512}
	got, err := fetchOnce(t, f, srv.URL)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(got) > 700 {
		t.Errorf("body = %d bytes, want it capped near 512", len(got))
	}
	if !strings.Contains(got, "truncated") {
		t.Error("a truncated body must say so")
	}
}

func TestFetchReportsAnErrorStatusWithTheBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, "no such thing")
	}))
	defer srv.Close()
	got, err := fetchOnce(t, &Fetcher{AllowPrivate: true}, srv.URL)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !strings.Contains(got, "HTTP 404") || !strings.Contains(got, "no such thing") {
		t.Errorf("result = %q, want the status and what the server said", got)
	}
}

func TestFetchRefusesBinary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "image/png")
		w.Write([]byte{0x89, 0x50, 0x4e, 0x47, 0x00, 0x01})
	}))
	defer srv.Close()
	if _, err := fetchOnce(t, &Fetcher{AllowPrivate: true}, srv.URL); err == nil {
		t.Error("a binary body should be refused rather than mangled into the context")
	}
}

func TestFetchDeclaresItsTraits(t *testing.T) {
	tr, ok := golm.TraitsOf((&Fetcher{}).Tool())
	if !ok {
		t.Fatal("fetch should declare its traits")
	}
	if !tr.Network || !tr.ReadOnly {
		t.Errorf("traits = %+v, want network and read-only", tr)
	}
}

// ParallelTools runs a turn's calls at once.
func TestFetcherIsSafeForConcurrentUse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer srv.Close()
	f := &Fetcher{AllowPrivate: true}
	tool := f.Tool()
	b, _ := json.Marshal(fetchArgs{URL: srv.URL})
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := tool.Execute(context.Background(), b); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent fetch: %v", err)
	}
}
