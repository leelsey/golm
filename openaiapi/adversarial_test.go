// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package openaiapi_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

// A server answers whatever is sent to it.
func TestHostileRequestBodies(t *testing.T) {
	_, ts, _ := newServer(t)
	bodies := map[string]string{
		"empty":               ``,
		"not json":            `this is not json`,
		"null":                `null`,
		"array":               `[1,2,3]`,
		"deeply nested":       `{"model":"assistant","messages":[{"role":"user","content":` + strings.Repeat(`[{"type":"text","text":"x"},`, 50) + `[]` + strings.Repeat(`]`, 50) + `}]}`,
		"wrong types":         `{"model":123,"messages":"not a list"}`,
		"content is a number": `{"model":"assistant","messages":[{"role":"user","content":42}]}`,
		"null content":        `{"model":"assistant","messages":[{"role":"user","content":null}]}`,
		"missing role":        `{"model":"assistant","messages":[{"content":"hi"}]}`,
		"huge role":           `{"model":"assistant","messages":[{"role":"` + strings.Repeat("x", 4096) + `","content":"hi"}]}`,
		"nul in content":      "{\"model\":\"assistant\",\"messages\":[{\"role\":\"user\",\"content\":\"a\\u0000b\"}]}",
		"trailing garbage":    `{"model":"assistant","messages":[{"role":"user","content":"hi"}]} trailing`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions",
				bytes.NewReader([]byte(body)))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := ts.Client().Do(req)
			if err != nil {
				t.Fatalf("the server did not answer: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode >= 500 {
				t.Errorf("status %d for %s; a bad request is not a server error", resp.StatusCode, name)
			}

			after := post(t, ts, "/v1/chat/completions", chat(user("still here?")), "")
			defer after.Body.Close()
			if after.StatusCode != http.StatusOK {
				t.Errorf("the server stopped answering after %s: status %d", name, after.StatusCode)
			}
		})
	}
}

// A streaming request that hits the same shapes must still terminate the stream.
func TestHostileStreamingRequests(t *testing.T) {
	_, ts, _ := newServer(t)
	for _, body := range []string{
		`{"model":"assistant","messages":[{"role":"user","content":"hi"}],"stream":true,"n":5}`,
		`{"model":"nosuch","messages":[{"role":"user","content":"hi"}],"stream":true}`,
		`{"model":"assistant","messages":[],"stream":true}`,
	} {
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions",
			bytes.NewReader([]byte(body)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("no answer: %v", err)
		}

		if ct := resp.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/event-stream") {
			events := readSSE(t, resp)
			if len(events) == 0 || events[len(events)-1] != "[DONE]" {
				t.Errorf("a refused streaming request left the stream open: %v", events)
			}
			continue
		}
		if resp.StatusCode < 400 {
			t.Errorf("body %q was accepted with status %d", body, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

// Every method/path combination the router does not serve must be refused cleanly rather than reaching a handler that expects a body.
func TestUnservedRoutes(t *testing.T) {
	_, ts, _ := newServer(t)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/v1/chat/completions"},
		{http.MethodPost, "/v1/models"},
		{http.MethodDelete, "/v1/models/assistant"},
		{http.MethodPost, "/health"},
		{http.MethodGet, "/v1/nonsense"},
		{http.MethodGet, "/"},
	} {
		req, err := http.NewRequest(c.method, ts.URL+c.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", c.method, c.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode >= 500 {
			t.Errorf("%s %s returned %d", c.method, c.path, resp.StatusCode)
		}
	}
}
