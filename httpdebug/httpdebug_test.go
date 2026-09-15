// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package httpdebug

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDumpAndRedact(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	var log bytes.Buffer
	c := NewClient(&log)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/x?api_key=sk-secret", strings.NewReader(`{"q":1}`))
	req.Header.Set("Authorization", "Bearer sk-secret")
	req.Header.Set("X-Api-Key", "sk-secret")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != `{"ok":true}` {
		t.Errorf("body altered by tee: %q", body)
	}

	s := log.String()
	if strings.Contains(s, "sk-secret") {
		t.Fatalf("credential leaked into log:\n%s", s)
	}
	for _, want := range []string{
		">> [1] POST", "api_key=redacted",
		"Authorization: [redacted]", "X-Api-Key: [redacted]",
		"Content-Type: application/json",
		`body (7 bytes): {"q":1}`,
		"<< [1] 200 OK", `{"ok":true}`, "body end (11 bytes)",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("log missing %q:\n%s", want, s)
		}
	}
}

func TestBodyCap(t *testing.T) {
	big := strings.Repeat("a", 100)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, big)
	}))
	defer srv.Close()

	var log bytes.Buffer
	c := &http.Client{Transport: &Transport{W: &log, MaxBody: 10}}
	req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(big))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(got) != big {
		t.Errorf("cap must not truncate the real body (got %d bytes)", len(got))
	}
	s := log.String()
	if !strings.Contains(s, "…(capped, 100 bytes total)") {
		t.Errorf("request cap marker missing:\n%s", s)
	}
	if !strings.Contains(s, "body log capped") || !strings.Contains(s, "body end (100 bytes)") {
		t.Errorf("response cap/end markers missing:\n%s", s)
	}
	if strings.Count(s, "aaaaaaaaaaa") > 0 {
		t.Errorf("logged body exceeds cap:\n%s", s)
	}
}

func TestRedactURLUserinfo(t *testing.T) {
	var log bytes.Buffer
	c := NewClient(&log)

	_, _ = c.Get("http://user:hunter2@127.0.0.1:0/v1?api_key=sk-x&alt=sse")
	s := log.String()
	if strings.Contains(s, "hunter2") || strings.Contains(s, "sk-x") {
		t.Fatalf("credentials leaked:\n%s", s)
	}
	if !strings.Contains(s, "redacted@127.0.0.1:0") || !strings.Contains(s, "alt=sse") {
		t.Errorf("expected redacted userinfo and preserved params:\n%s", s)
	}
}

func TestTransportError(t *testing.T) {
	var log bytes.Buffer
	c := NewClient(&log)
	if _, err := c.Get("http://127.0.0.1:0"); err == nil {
		t.Fatal("expected connection error")
	}
	if !strings.Contains(log.String(), "<< [1] error after") {
		t.Errorf("error line missing:\n%s", log.String())
	}
}

func TestIDsIncrement(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()
	var log bytes.Buffer
	c := NewClient(&log)
	for i := 0; i < 2; i++ {
		resp, err := c.Get(srv.URL)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	if !strings.Contains(log.String(), ">> [2] GET") {
		t.Errorf("second request id missing:\n%s", log.String())
	}
}

// A proxy — or a hostile endpoint an agent was told to reach.
func TestResponseBodyIsScrubbedOfCredentials(t *testing.T) {
	const key = "sk-SUPERSECRET-0123456789"
	for _, header := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key"} {
		t.Run(header, func(t *testing.T) {
			value := key
			if header == "Authorization" {
				value = "Bearer " + key
			}
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"error":"rejected ` + r.Header.Get(header) + ` (` + key + `)"}`))
			}))
			defer ts.Close()

			var out bytes.Buffer
			c := NewClient(&out)
			req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set(header, value)
			resp, err := c.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}

			if !strings.Contains(string(body), key) {
				t.Errorf("the response reaching the caller was altered: %q", body)
			}
			if strings.Contains(out.String(), key) {
				t.Errorf("the key is in the debug dump:\n%s", out.String())
			}
		})
	}
}
