// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

//go:build live

package golm_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/provider/anthropic"
)

func runClaude(t *testing.T, prompt string) string {
	t.Helper()
	cmd := exec.Command("claude", "-p", "--model", "haiku")
	cmd.Stdin = strings.NewReader(prompt)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("claude: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func flattenBlocks(raw json.RawMessage) string {
	var blocks []map[string]any
	_ = json.Unmarshal(raw, &blocks)
	var parts []string
	for _, b := range blocks {
		switch b["type"] {
		case "text":
			parts = append(parts, fmt.Sprint(b["text"]))
		case "tool_result":
			parts = append(parts, "TOOL_RESULT="+fmt.Sprint(b["content"]))
		case "tool_use":
			parts = append(parts, "(you previously called "+fmt.Sprint(b["name"])+")")
		}
	}
	return strings.Join(parts, " ")
}

func claudeShim(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			System   string `json:"system"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		var b strings.Builder
		b.WriteString("You are the assistant in a tool-using agent loop. Follow the protocol exactly.\n")
		if req.System != "" {
			b.WriteString("System instructions: " + req.System + "\n")
		}
		if len(req.Tools) > 0 {
			b.WriteString("Tools you may call:\n")
			for _, tl := range req.Tools {
				b.WriteString("- " + tl.Name + ": " + tl.Description + "\n")
			}
			b.WriteString("\nPROTOCOL: If you need a tool, output ONE line and nothing else:\n")
			b.WriteString("TOOL <toolName> <compact-json-arguments>\n")
			b.WriteString("Example: TOOL add {\"a\":2,\"b\":3}\n")
			b.WriteString("Once a line containing TOOL_RESULT= is present, do NOT call a tool again; output only the final answer.\n")
		}
		b.WriteString("\nConversation so far:\n")
		for _, m := range req.Messages {
			b.WriteString(m.Role + ": " + flattenBlocks(m.Content) + "\n")
		}
		b.WriteString("\nYour response:")

		out := runClaude(t, b.String())

		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(strings.Trim(line, "`"))
			if strings.HasPrefix(line, "TOOL ") {
				rest := strings.TrimSpace(strings.TrimPrefix(line, "TOOL "))
				name, args, _ := strings.Cut(rest, " ")
				args = strings.TrimSpace(args)
				if args == "" {
					args = "{}"
				}
				writeJSON(w, map[string]any{
					"id": "msg", "role": "assistant", "stop_reason": "tool_use",
					"content": []any{map[string]any{"type": "tool_use", "id": "t1", "name": name, "input": json.RawMessage(args)}},
					"usage":   map[string]int{"input_tokens": 1, "output_tokens": 1},
				})
				return
			}
		}
		writeJSON(w, map[string]any{
			"id": "msg", "role": "assistant", "stop_reason": "end_turn",
			"content": []any{map[string]any{"type": "text", "text": out}},
			"usage":   map[string]int{"input_tokens": 1, "output_tokens": 1},
		})
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("content-type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestLiveClaudeBackedHTTPToolLoop(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not installed")
	}
	srv := httptest.NewServer(claudeShim(t))
	defer srv.Close()

	reg := golm.NewRegistry()
	reg.Register(golm.NewTool("add", "add two integers a and b and return their sum",
		json.RawMessage(`{"type":"object","properties":{"a":{"type":"integer"},"b":{"type":"integer"}},"required":["a","b"]}`),
		func(_ context.Context, in json.RawMessage) (string, error) {
			var args struct{ A, B int }
			if err := json.Unmarshal(in, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("%d", args.A+args.B), nil
		}))

	sess := golm.NewSession()
	agent := &golm.Agent{
		Provider: anthropic.New("shim").WithBaseURL(srv.URL),
		Model:    "claude-haiku", Tools: reg, MaxSteps: 5,
	}
	res, err := agent.Run(context.Background(), sess, "Use the add tool to compute 2 + 3, then reply with only the number.")
	if err != nil {
		t.Fatalf("agent.Run: %v", err)
	}
	t.Logf("final answer: %q", res.Text())

	calledTool := false
	for _, m := range sess.History() {
		for _, c := range m.Content {
			if tu, ok := c.(golm.ToolUse); ok && tu.Name == "add" {
				calledTool = true
			}
		}
	}
	if !calledTool {
		t.Error("the model never called the add tool through the HTTP adapter + ReAct loop")
	}
	if !strings.Contains(res.Text(), "5") {
		t.Errorf("final answer should contain 5, got %q", res.Text())
	}
}
