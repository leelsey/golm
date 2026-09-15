// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package openaiapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/openaiapi"
	"github.com/leelsey/golm/sessionstore"
)

type failing struct {
	stop golm.StopReason
	err  error
}

func (f *failing) Name() string                    { return "failing" }
func (f *failing) Capabilities() golm.Capabilities { return golm.Capabilities{Streaming: true} }

func (f *failing) Complete(context.Context, golm.Request) (golm.Response, error) {
	if f.err != nil {
		return golm.Response{Usage: golm.Usage{InputTokens: 5}}, f.err
	}
	return golm.Response{Message: golm.AssistantText(""), StopReason: f.stop,
		Usage: golm.Usage{InputTokens: 5}}, nil
}

func (f *failing) Stream(ctx context.Context, req golm.Request, _ func(golm.StreamEvent) error) (golm.Response, error) {
	return f.Complete(ctx, req)
}

func serverFor(t *testing.T, p golm.Provider) *httptest.Server {
	t.Helper()
	s := openaiapi.NewServer()
	s.Add("assistant", &golm.Agent{Provider: p, Model: "m", MaxSteps: 2}, "")
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// A refusal and an exhausted context window have no OpenAI spelling.
func TestTerminalStopsBecomeErrors(t *testing.T) {
	cases := map[golm.StopReason]struct {
		status int
		code   string
	}{
		golm.StopRefusal:         {http.StatusBadRequest, "content_filter"},
		golm.StopContextOverflow: {http.StatusBadRequest, "context_length_exceeded"},
	}
	for stop, want := range cases {
		t.Run(string(stop), func(t *testing.T) {
			ts := serverFor(t, &failing{stop: stop})
			resp := post(t, ts, "/v1/chat/completions", chat(user("hi")), "")
			defer resp.Body.Close()
			if resp.StatusCode != want.status {
				t.Fatalf("status %d, want %d", resp.StatusCode, want.status)
			}
			var out map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				t.Fatal(err)
			}
			e, ok := out["error"].(map[string]any)
			if !ok {
				t.Fatalf("no error body: %v", out)
			}
			if e["code"] != want.code {
				t.Errorf("code = %v, want %q", e["code"], want.code)
			}
		})
	}
}

func TestProviderErrorIsABadGateway(t *testing.T) {
	ts := serverFor(t, &failing{err: errors.New("upstream exploded")})
	resp := post(t, ts, "/v1/chat/completions", chat(user("hi")), "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status %d, want 502", resp.StatusCode)
	}
}

// The status line goes out with the first chunk.
func TestStreamErrorStillTerminates(t *testing.T) {
	ts := serverFor(t, &failing{stop: golm.StopRefusal})
	resp := post(t, ts, "/v1/chat/completions", map[string]any{
		"model": "assistant", "messages": []map[string]any{user("hi")}, "stream": true,
	}, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a stream that fails part-way should still open: %d", resp.StatusCode)
	}
	events := readSSE(t, resp)
	if len(events) == 0 || events[len(events)-1] != "[DONE]" {
		t.Fatalf("stream did not terminate: %v", events)
	}
	var sawError bool
	for _, e := range events[:len(events)-1] {
		var chunk map[string]any
		if err := json.Unmarshal([]byte(e), &chunk); err != nil {
			continue
		}
		if body, ok := chunk["error"].(map[string]any); ok {
			sawError = true
			if body["code"] != "content_filter" {
				t.Errorf("error chunk = %v", body)
			}
		}
	}
	if !sawError {
		t.Error("the failure was never reported in the stream")
	}
}

// A delegated sub-agent's working is progress, not content.
func TestSubAgentOutputIsNotStreamedAsContent(t *testing.T) {
	s := openaiapi.NewServer()
	o := golm.NewOrchestrator(nil)
	o.StreamDelegates = true
	o.Add("main", "main", &golm.Agent{Model: "m", Provider: &delegator{}})
	o.Add("helper", "sub", &golm.Agent{Model: "s", Provider: &echoProvider{}})
	if err := o.WireDelegation(nil); err != nil {
		t.Fatal(err)
	}
	s.Add("team", o, "a team")
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := post(t, ts, "/v1/chat/completions", map[string]any{
		"model": "team", "messages": []map[string]any{user("go")}, "stream": true,
	}, "")
	var content strings.Builder
	for _, e := range readSSE(t, resp) {
		if e == "[DONE]" {
			continue
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(e), &chunk); err != nil {
			continue
		}
		choices, _ := chunk["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		if d, ok := choices[0].(map[string]any)["delta"].(map[string]any); ok {
			if s, ok := d["content"].(string); ok {
				content.WriteString(s)
			}
		}
	}
	if strings.Contains(content.String(), "echo(") {
		t.Errorf("a sub-agent's working reached the client as content: %q", content.String())
	}
	if !strings.Contains(content.String(), "coordinated") {
		t.Errorf("the answer is missing: %q", content.String())
	}
}

type delegator struct{ calls int }

func (d *delegator) Name() string { return "delegator" }
func (d *delegator) Capabilities() golm.Capabilities {
	return golm.Capabilities{Streaming: true, Tools: true}
}

func (d *delegator) Complete(context.Context, golm.Request) (golm.Response, error) {
	d.calls++
	if d.calls == 1 {
		return golm.Response{Message: golm.Message{Role: golm.RoleAssistant, Content: []golm.Content{
			golm.ToolUse{ID: "h1", Name: "helper", Input: json.RawMessage(`{"task":"look"}`)},
		}}, StopReason: golm.StopToolUse}, nil
	}
	return golm.Response{Message: golm.AssistantText("coordinated"), StopReason: golm.StopEndTurn}, nil
}

func (d *delegator) Stream(ctx context.Context, req golm.Request, fn func(golm.StreamEvent) error) (golm.Response, error) {
	resp, err := d.Complete(ctx, req)
	if err == nil {
		if txt := resp.Message.Text(); txt != "" {
			_ = fn(golm.StreamEvent{Type: golm.EventTextDelta, Text: txt})
		}
	}
	return resp, err
}

// A conversation name the store cannot address is the client's mistake.
func TestInvalidSessionNameIsAClientError(t *testing.T) {
	a := &golm.Agent{Provider: &echoProvider{}, Model: "m", MaxSteps: 2}
	s := openaiapi.NewServer()
	s.Store = &sessionstore.Files{Dir: t.TempDir()}
	s.Add("m", a, "")
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	body := `{"model":"m","golm_session":"../../etc/passwd","messages":[{"role":"user","content":"hi"}]}`
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status %d, want 400", resp.StatusCode)
	}
	var out struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Error.Code != "invalid_session" {
		t.Errorf("code %q, want invalid_session", out.Error.Code)
	}
}

// The store is still the authority on what it accepts.
func TestValidSessionNameStillWorks(t *testing.T) {
	a := &golm.Agent{Provider: &echoProvider{}, Model: "m", MaxSteps: 2}
	s := openaiapi.NewServer()
	s.Store = &sessionstore.Files{Dir: t.TempDir()}
	s.Add("m", a, "")
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	body := `{"model":"m","golm_session":"chat-1","messages":[{"role":"user","content":"hi"}]}`
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status %d, want 200", resp.StatusCode)
	}
}
