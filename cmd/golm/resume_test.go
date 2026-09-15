// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type sentMessage struct {
	Role       string `json:"role"`
	Content    any    `json:"content"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolCalls  []struct {
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls,omitempty"`
}

type recorder struct {
	mu    sync.Mutex
	sent  [][]sentMessage
	turns int
}

func (rec *recorder) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []sentMessage `json:"messages"`
		}
		if err := json.Unmarshal(b, &req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		rec.mu.Lock()
		rec.sent = append(rec.sent, req.Messages)
		n := rec.turns
		rec.turns++
		rec.mu.Unlock()

		w.Header().Set("content-type", "application/json")
		if n == 0 {
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[
				{"id":"call_1","type":"function","function":{"name":"read_skill","arguments":"{\"name\":\"triage\"}"}}]},
				"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":3}}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":20,"completion_tokens":4}}`)
	})
}

func (rec *recorder) request(i int) []sentMessage {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.sent[i]
}

func skillDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	d := filepath.Join(dir, "triage")
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	doc := "---\nname: triage\ndescription: sort incoming reports\n---\nthe triage instructions\n"
	if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A resumed session has to reach the model as the conversation it was.
func TestResumedSessionReachesTheModelIntact(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler(t))
	defer srv.Close()
	t.Setenv("GOLM_TEST_KEY", "k")

	store := t.TempDir()
	skills := skillDir(t)
	base := []string{
		"--provider", "openai", "--base-url", srv.URL + "/v1",
		"--api-key-env", "GOLM_TEST_KEY", "--model", "gpt-4o",
		"--skills", skills, "--store", store, "--session", "chat", "--no-stream",
	}
	var out, errOut bytes.Buffer
	if code := run(append(base, "first"), strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("first run = %d: %s", code, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := run(append(base, "second"), strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("second run = %d: %s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "resumed session chat") {
		t.Errorf("stderr = %q, want the resume announced", errOut.String())
	}

	msgs := rec.request(2)
	var roles []string
	for _, m := range msgs {
		if m.Role == "system" {
			continue
		}
		roles = append(roles, m.Role)
	}
	want := []string{"user", "assistant", "tool", "assistant", "user"}
	if len(roles) != len(want) {
		t.Fatalf("resumed transcript roles = %v, want %v", roles, want)
	}
	for i := range want {
		if roles[i] != want[i] {
			t.Fatalf("resumed transcript roles = %v, want %v", roles, want)
		}
	}

	var callID, resultID string
	for _, m := range msgs {
		if len(m.ToolCalls) > 0 {
			callID = m.ToolCalls[0].ID
			if got := m.ToolCalls[0].Function.Name; got != "read_skill" {
				t.Errorf("tool name = %q, want read_skill", got)
			}
		}
		if m.Role == "tool" {
			resultID = m.ToolCallID
		}
	}
	if callID == "" || callID != resultID {
		t.Errorf("tool_call id %q and tool_call_id %q must match", callID, resultID)
	}

	for _, m := range msgs {
		if m.Role != "tool" {
			continue
		}
		if s, ok := m.Content.(string); !ok || !strings.Contains(s, "triage instructions") {
			t.Errorf("tool result = %v, want the skill body", m.Content)
		}
	}
}

// The skills index belongs in the system prompt, once, however many turns the conversation has run for.
func TestSkillsIndexIsSentOnceNotPerTurn(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler(t))
	defer srv.Close()
	t.Setenv("GOLM_TEST_KEY", "k")

	args := []string{
		"--provider", "openai", "--base-url", srv.URL + "/v1",
		"--api-key-env", "GOLM_TEST_KEY", "--model", "gpt-4o",
		"--skills", skillDir(t), "--no-stream", "hello",
	}
	var out, errOut bytes.Buffer
	if code := run(args, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("run = %d: %s", code, errOut.String())
	}

	msgs := rec.request(0)
	systems := 0
	for _, m := range msgs {
		if m.Role == "system" {
			systems++
			s, _ := m.Content.(string)
			if !strings.Contains(s, "triage: sort incoming reports") {
				t.Errorf("system prompt = %q, want the skills index", s)
			}
			if strings.Contains(s, "triage instructions") {
				t.Error("the body must not be in the prompt; that is what read_skill is for")
			}
		}
	}
	if systems != 1 {
		t.Errorf("system messages = %d, want exactly 1", systems)
	}
}
