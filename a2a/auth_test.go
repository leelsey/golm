// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package a2a

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func authTestServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	s := NewServer(AgentCard{Name: "t"}, echoHandler{})
	s.AuthToken = token
	srv := httptest.NewServer(s.HTTPHandler())
	t.Cleanup(srv.Close)
	return srv
}

func rpc(t *testing.T, url, bearer string) int {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"message/send","params":{"message":{"role":"user","parts":[{"kind":"text","text":"hi"}]}}}`
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestServerAuthToken(t *testing.T) {
	srv := authTestServer(t, "s3cret")

	if got := rpc(t, srv.URL, ""); got != http.StatusUnauthorized {
		t.Errorf("no token: status %d, want 401", got)
	}
	if got := rpc(t, srv.URL, "wrong"); got != http.StatusUnauthorized {
		t.Errorf("wrong token: status %d, want 401", got)
	}
	if got := rpc(t, srv.URL, "s3cret"); got != http.StatusOK {
		t.Errorf("correct token: status %d, want 200", got)
	}
}

func TestServerWithoutAuthTokenStaysOpen(t *testing.T) {
	srv := authTestServer(t, "")
	if got := rpc(t, srv.URL, ""); got != http.StatusOK {
		t.Errorf("an unset AuthToken must keep the previous behaviour, got %d", got)
	}
}

// Discovery is the one thing a peer must be able to do before it holds a credential.
func TestAgentCardIsNotGated(t *testing.T) {
	srv := authTestServer(t, "s3cret")
	resp, err := http.Get(srv.URL + wellKnownPath)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("agent card status %d, want 200", resp.StatusCode)
	}
}

// The other half of the token.
func TestClientSendsTheToken(t *testing.T) {
	srv := authTestServer(t, "s3cret")

	if _, err := NewClient(srv.URL).SendMessage(context.Background(), "hi"); err == nil {
		t.Error("a client with no token must not reach a protected server")
	}
	parts, err := NewClient(srv.URL).WithToken("s3cret").SendMessage(context.Background(), "hi")
	if err != nil {
		t.Fatalf("a client with the right token must get through: %v", err)
	}
	if len(parts) == 0 {
		t.Error("no parts returned")
	}
	if _, err := NewClient(srv.URL).WithToken("wrong").SendMessage(context.Background(), "hi"); err == nil {
		t.Error("a wrong token must not be accepted")
	}
}

// An unauthenticated server must stay reachable by a client that has no token.
func TestClientWithoutTokenStillWorksUnauthenticated(t *testing.T) {
	srv := authTestServer(t, "")
	if _, err := NewClient(srv.URL).SendMessage(context.Background(), "hi"); err != nil {
		t.Errorf("existing tokenless clients must keep working: %v", err)
	}
}
