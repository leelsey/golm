// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package openaiapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/openaiapi"
	"github.com/leelsey/golm/sessionstore"
)

type echoProvider struct{ calls atomic.Int64 }

func (e *echoProvider) Name() string                    { return "echo" }
func (e *echoProvider) Capabilities() golm.Capabilities { return golm.Capabilities{Streaming: true} }

func (e *echoProvider) Complete(_ context.Context, req golm.Request) (golm.Response, error) {
	e.calls.Add(1)
	var last string
	users := 0
	for _, m := range req.Messages {
		if m.Role == golm.RoleUser {
			users++
			last = m.Text()
		}
	}
	return golm.Response{
		Message:    golm.AssistantText(fmt.Sprintf("echo(%d users):%s", users, last)),
		StopReason: golm.StopEndTurn,
		Usage:      golm.Usage{InputTokens: 11, OutputTokens: 7, ThinkingTokens: 3, CacheReadTokens: 5},
	}, nil
}

func (e *echoProvider) Stream(ctx context.Context, req golm.Request, fn func(golm.StreamEvent) error) (golm.Response, error) {
	resp, err := e.Complete(ctx, req)
	if err != nil {
		return resp, err
	}
	for _, word := range strings.SplitAfter(resp.Message.Text(), " ") {
		if word == "" {
			continue
		}
		if err := fn(golm.StreamEvent{Type: golm.EventTextDelta, Text: word}); err != nil {
			return golm.Response{}, err
		}
	}
	return resp, nil
}

func newServer(t *testing.T) (*openaiapi.Server, *httptest.Server, *echoProvider) {
	t.Helper()
	p := &echoProvider{}
	s := openaiapi.NewServer()
	s.Add("assistant", &golm.Agent{Provider: p, Model: "m", Name: "assistant"}, "answers questions")
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts, p
}

func post(t *testing.T, ts *httptest.Server, path string, body any, token string) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+path, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func chat(msgs ...map[string]any) map[string]any {
	return map[string]any{"model": "assistant", "messages": msgs}
}

func user(text string) map[string]any { return map[string]any{"role": "user", "content": text} }

func decode[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return v
}

// The whole point: an ordinary OpenAI client gets an ordinary OpenAI answer.
func TestChatCompletion(t *testing.T) {
	_, ts, _ := newServer(t)
	resp := post(t, ts, "/v1/chat/completions", chat(user("hello")), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	out := decode[map[string]any](t, resp)
	if out["object"] != "chat.completion" {
		t.Errorf("object = %v", out["object"])
	}
	if !strings.HasPrefix(out["id"].(string), "chatcmpl-") {
		t.Errorf("id = %v", out["id"])
	}
	choices := out["choices"].([]any)
	if len(choices) != 1 {
		t.Fatalf("choices = %v", choices)
	}
	c := choices[0].(map[string]any)
	msg := c["message"].(map[string]any)
	if msg["role"] != "assistant" || !strings.Contains(msg["content"].(string), "hello") {
		t.Errorf("message = %v", msg)
	}
	if c["finish_reason"] != "stop" {
		t.Errorf("finish_reason = %v", c["finish_reason"])
	}

	u := out["usage"].(map[string]any)
	if u["prompt_tokens"] != 11.0 || u["completion_tokens"] != 10.0 || u["total_tokens"] != 21.0 {
		t.Errorf("usage = %v", u)
	}

	details := u["prompt_tokens_details"].(map[string]any)
	if details["cached_tokens"] != 5.0 {
		t.Errorf("cached tokens = %v", details)
	}
	if d := u["completion_tokens_details"].(map[string]any); d["reasoning_tokens"] != 3.0 {
		t.Errorf("reasoning tokens = %v", d)
	}
}

// Clients differ on whether the base URL already ends in /v1.
func TestBothPathPrefixesWork(t *testing.T) {
	_, ts, _ := newServer(t)
	for _, path := range []string{"/v1/chat/completions", "/chat/completions"} {
		resp := post(t, ts, path, chat(user("hi")), "")
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status %d", path, resp.StatusCode)
		}
		resp.Body.Close()
	}
	for _, path := range []string{"/v1/models", "/models"} {
		resp, err := ts.Client().Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status %d", path, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

// The neat part: a registered name is a persona, not a model.
func TestModelsListsThePersonas(t *testing.T) {
	s, ts, _ := newServer(t)
	s.Add("researcher", &golm.Agent{Provider: &echoProvider{}, Model: "m"}, "looks things up")

	resp, err := ts.Client().Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	out := decode[map[string]any](t, resp)
	if out["object"] != "list" {
		t.Errorf("object = %v", out["object"])
	}
	data := out["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("models = %v", data)
	}
	first := data[0].(map[string]any)
	if first["id"] != "assistant" || first["object"] != "model" || first["owned_by"] != "golm" {
		t.Errorf("model info = %v", first)
	}
	if first["golm_description"] != "answers questions" {
		t.Errorf("description lost: %v", first)
	}

	one, err := ts.Client().Get(ts.URL + "/v1/models/researcher")
	if err != nil {
		t.Fatal(err)
	}
	if got := decode[map[string]any](t, one); got["id"] != "researcher" {
		t.Errorf("single model = %v", got)
	}
	missing, err := ts.Client().Get(ts.URL + "/v1/models/nobody")
	if err != nil {
		t.Fatal(err)
	}
	defer missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Errorf("unknown model status %d", missing.StatusCode)
	}
}

func TestUnknownModelNamesTheOnesServed(t *testing.T) {
	_, ts, _ := newServer(t)
	resp := post(t, ts, "/v1/chat/completions",
		map[string]any{"model": "gpt-9", "messages": []map[string]any{user("hi")}}, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d", resp.StatusCode)
	}
	out := decode[map[string]any](t, resp)
	e := out["error"].(map[string]any)
	if !strings.Contains(e["message"].(string), "assistant") {
		t.Errorf("the error should say what IS served: %v", e)
	}
}

// A client that offers tools is waiting for tool calls.
func TestCallerSuppliedToolsAreRefused(t *testing.T) {
	_, ts, _ := newServer(t)
	for name, body := range map[string]map[string]any{
		"tools": {"model": "assistant", "messages": []map[string]any{user("hi")},
			"tools": []map[string]any{{"type": "function", "function": map[string]any{"name": "f"}}}},
		"functions": {"model": "assistant", "messages": []map[string]any{user("hi")},
			"functions": []map[string]any{{"name": "f"}}},
		"tool_choice": {"model": "assistant", "messages": []map[string]any{user("hi")},
			"tool_choice": "auto"},
		"n": {"model": "assistant", "messages": []map[string]any{user("hi")}, "n": 3},
	} {
		t.Run(name, func(t *testing.T) {
			resp := post(t, ts, "/v1/chat/completions", body, "")
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status %d, want a refusal", resp.StatusCode)
			}
			out := decode[map[string]any](t, resp)
			if out["error"] == nil {
				t.Errorf("no error body: %v", out)
			}
		})
	}
}

// Sampling belongs to the persona.
func TestSamplingParametersAreAccepted(t *testing.T) {
	_, ts, _ := newServer(t)
	resp := post(t, ts, "/v1/chat/completions", map[string]any{
		"model": "assistant", "messages": []map[string]any{user("hi")},
		"temperature": 0.2, "max_tokens": 100, "top_p": 0.9, "user": "u-1",
	}, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status %d: sampling knobs should not be refused", resp.StatusCode)
	}
}

// The conversation the client sends is the conversation the agent sees.
func TestHistoryIsCarried(t *testing.T) {
	_, ts, _ := newServer(t)
	resp := post(t, ts, "/v1/chat/completions", chat(
		map[string]any{"role": "system", "content": "be terse"},
		user("first"),
		map[string]any{"role": "assistant", "content": "ok"},
		user("second"),
	), "")
	out := decode[map[string]any](t, resp)
	msg := out["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)

	if !strings.Contains(msg["content"].(string), "echo(2 users):second") {
		t.Errorf("history not carried: %v", msg["content"])
	}
}

func TestContentPartsArrayIsAccepted(t *testing.T) {
	_, ts, _ := newServer(t)
	resp := post(t, ts, "/v1/chat/completions", map[string]any{
		"model": "assistant",
		"messages": []map[string]any{{
			"role":    "user",
			"content": []map[string]any{{"type": "text", "text": "parts form"}},
		}},
	}, "")
	out := decode[map[string]any](t, resp)
	msg := out["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if !strings.Contains(msg["content"].(string), "parts form") {
		t.Errorf("content parts not read: %v", msg)
	}
}

func TestMalformedRequests(t *testing.T) {
	_, ts, _ := newServer(t)
	cases := map[string]any{
		"no messages":       map[string]any{"model": "assistant", "messages": []map[string]any{}},
		"assistant is last": chat(user("hi"), map[string]any{"role": "assistant", "content": "x"}),
		"empty turn":        chat(user("   ")),
		"unknown role":      chat(map[string]any{"role": "wizard", "content": "x"}, user("hi")),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			resp := post(t, ts, "/v1/chat/completions", body, "")
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status %d, want 400", resp.StatusCode)
			}
			resp.Body.Close()
		})
	}
}

func TestAuthToken(t *testing.T) {
	s, ts, _ := newServer(t)
	s.AuthToken = "s3cret"
	if resp := post(t, ts, "/v1/chat/completions", chat(user("hi")), ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: status %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
	if resp := post(t, ts, "/v1/chat/completions", chat(user("hi")), "wrong"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong token: status %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
	resp := post(t, ts, "/v1/chat/completions", chat(user("hi")), "s3cret")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("right token: status %d", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/models", nil)
	r2, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusUnauthorized {
		t.Errorf("models without a token: status %d", r2.StatusCode)
	}
}

func TestBodyLimit(t *testing.T) {
	s, ts, _ := newServer(t)
	s.MaxBody = 128
	resp := post(t, ts, "/v1/chat/completions", chat(user(strings.Repeat("x", 4096))), "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status %d, want 413", resp.StatusCode)
	}
}

// An AGENT has more state than a transcript.
func TestNamedSessionIsStateful(t *testing.T) {
	p := &echoProvider{}
	s := openaiapi.NewServer()
	s.Add("assistant", &golm.Agent{Provider: p, Model: "m"}, "")
	s.Store = sessionstore.NewFiles(t.TempDir())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	for i, text := range []string{"first", "second"} {
		body := map[string]any{
			"model": "assistant", "golm_session": "CONVERSATION",
			"messages": []map[string]any{user(text)},
		}
		resp := post(t, ts, "/v1/chat/completions", body, "")
		out := decode[map[string]any](t, resp)
		msg := out["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
		want := fmt.Sprintf("echo(%d users):%s", i+1, text)
		if !strings.Contains(msg["content"].(string), want) {
			t.Errorf("turn %d: %v, want %q — the session did not carry", i, msg["content"], want)
		}
	}
}

func TestHealth(t *testing.T) {
	_, ts, _ := newServer(t)
	resp, err := ts.Client().Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	out := decode[map[string]any](t, resp)
	if out["status"] != "ok" {
		t.Errorf("health = %v", out)
	}
}

func readSSE(t *testing.T, resp *http.Response) []string {
	t.Helper()
	defer resp.Body.Close()
	var out []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			out = append(out, data)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read stream: %v", err)
	}
	return out
}

func TestStreaming(t *testing.T) {
	_, ts, _ := newServer(t)
	resp := post(t, ts, "/v1/chat/completions", map[string]any{
		"model": "assistant", "messages": []map[string]any{user("hello there")},
		"stream": true, "stream_options": map[string]any{"include_usage": true},
	}, "")
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}
	events := readSSE(t, resp)
	if len(events) < 3 {
		t.Fatalf("events = %v", events)
	}

	if events[len(events)-1] != "[DONE]" {
		t.Fatalf("stream did not terminate with [DONE]: %v", events[len(events)-1])
	}
	var content strings.Builder
	var sawRole, sawFinish bool
	var usage map[string]any
	for _, e := range events[:len(events)-1] {
		var chunk map[string]any
		if err := json.Unmarshal([]byte(e), &chunk); err != nil {
			t.Fatalf("chunk is not JSON: %q", e)
		}
		if chunk["object"] != "chat.completion.chunk" {
			t.Errorf("object = %v", chunk["object"])
		}
		if u, ok := chunk["usage"].(map[string]any); ok {
			usage = u
			continue
		}
		choices := chunk["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		c := choices[0].(map[string]any)
		if d, ok := c["delta"].(map[string]any); ok {
			if d["role"] == "assistant" {
				sawRole = true
			}
			if s, ok := d["content"].(string); ok {
				content.WriteString(s)
			}
		}
		if c["finish_reason"] == "stop" {
			sawFinish = true
		}
	}

	if !sawRole {
		t.Error("no opening role chunk")
	}
	if !strings.Contains(content.String(), "hello there") {
		t.Errorf("streamed content = %q", content.String())
	}
	if !sawFinish {
		t.Error("no finish_reason chunk")
	}
	if usage == nil || usage["total_tokens"] != 21.0 {
		t.Errorf("include_usage produced %v", usage)
	}
}

func TestStreamingWithoutUsageOption(t *testing.T) {
	_, ts, _ := newServer(t)
	resp := post(t, ts, "/v1/chat/completions", map[string]any{
		"model": "assistant", "messages": []map[string]any{user("hi")}, "stream": true,
	}, "")
	for _, e := range readSSE(t, resp) {
		if strings.Contains(e, "total_tokens") {
			t.Errorf("usage sent without include_usage: %s", e)
		}
	}
}
