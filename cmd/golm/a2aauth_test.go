// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/a2a"
)

func TestAddrIsLoopback(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:8080": true,
		"localhost:8080": true,
		"[::1]:8080":     true,

		":8080":          false,
		"0.0.0.0:8080":   false,
		"192.168.1.5:80": false,
	} {
		if got := addrIsLoopback(addr); got != want {
			t.Errorf("addrIsLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
}

// --token-env naming an unset variable must stop the server rather than start it with no authentication.
func TestA2AServeRefusesEmptyTokenEnv(t *testing.T) {
	t.Setenv("GOLM_TEST_A2A_TOKEN", "")
	var stderr bytes.Buffer
	code := runA2AServe([]string{"--config", "unused.json", "--token-env", "GOLM_TEST_A2A_TOKEN"}, &stderr)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "empty or unset") {
		t.Errorf("stderr should name the empty token, got %q", stderr.String())
	}

	if strings.Contains(stderr.String(), "--config is required") {
		t.Error("the token check must run before the config is loaded")
	}
}

// The token reaching the server is one assignment.
func TestA2AServerForEnforcesTheToken(t *testing.T) {
	ag := &golm.Agent{Model: "x"}
	srv := httptest.NewServer(a2aServerFor(a2a.CardForAgent("t", "d", "http://x/"), ag, "s3cret").HTTPHandler())
	defer srv.Close()

	body := `{"jsonrpc":"2.0","id":1,"method":"tasks/get","params":{"id":"nope"}}`
	post := func(tok string) int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(body))
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if got := post(""); got != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", got)
	}
	if got := post("s3cret"); got != http.StatusOK {
		t.Errorf("correct token: %d, want 200", got)
	}
}

// And with no token configured the server must stay open.
func TestA2AServerForWithoutTokenStaysOpen(t *testing.T) {
	ag := &golm.Agent{Model: "x"}
	srv := httptest.NewServer(a2aServerFor(a2a.CardForAgent("t", "d", "http://x/"), ag, "").HTTPHandler())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/", "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tasks/get","params":{"id":"nope"}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		t.Error("an unconfigured token must not lock the server")
	}
}
