// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		out = append(out, m)
	}
	return out
}

func TestLoggerRecordsARunEndToEnd(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})

	reg := NewRegistry()
	reg.Register(echoTool())
	fp := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "t1", Name: "echo", Input: json.RawMessage(`{"x":1}`)},
		}}, StopReason: StopToolUse, Usage: Usage{InputTokens: 10, OutputTokens: 5}},
		{Message: AssistantText("done"), StopReason: StopEndTurn, Usage: Usage{InputTokens: 12, OutputTokens: 3}},
	}}
	a := &Agent{Provider: fp, Model: "m", Name: "worker", Tools: reg, Logger: slog.New(h)}
	if _, err := a.Run(context.Background(), NewSession(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	seen := map[string]map[string]any{}
	for _, l := range logLines(t, &buf) {
		ev, _ := l["event"].(string)
		seen[ev] = l
		if l["agent"] != "worker" {
			t.Errorf("event %q is not attributed to the agent: %v", ev, l["agent"])
		}
	}
	for _, want := range []string{"start", "step", "tool", "tool_result", "done"} {
		if seen[want] == nil {
			t.Errorf("no %q record; the log is not an account of the run", want)
		}
	}

	step := seen["step"]
	if step["total_tokens"] == nil || step["stop_reason"] == nil {
		t.Errorf("step record is missing accounting: %v", step)
	}

	if seen["step"]["level"] != "DEBUG" {
		t.Errorf("step level = %v, want DEBUG", seen["step"]["level"])
	}
	if seen["start"]["level"] != "INFO" || seen["done"]["level"] != "INFO" {
		t.Errorf("run boundaries should be INFO: %v / %v", seen["start"]["level"], seen["done"]["level"])
	}
}

// A tool that failed is the agent still working but not as intended.
func TestLoggerRaisesToolFailuresToWarn(t *testing.T) {
	var buf bytes.Buffer
	a := &Agent{
		Provider: &fakeProvider{responses: []Response{
			{Message: Message{Role: RoleAssistant, Content: []Content{
				ToolUse{ID: "t1", Name: "missing", Input: json.RawMessage(`{}`)},
			}}, StopReason: StopToolUse},
			{Message: AssistantText("ok"), StopReason: StopEndTurn},
		}},
		Model:  "m",
		Logger: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	if _, err := a.Run(context.Background(), NewSession(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var warned bool
	for _, l := range logLines(t, &buf) {
		if l["event"] == "tool_result" && l["level"] == "WARN" && l["is_error"] == true {
			warned = true
		}
	}
	if !warned {
		t.Error("a failing tool was recorded at Debug alongside the successful ones")
	}
}

// Whole answers and whole tool results are megabytes.
func TestLoggerBoundsWhatItRecords(t *testing.T) {
	var buf bytes.Buffer
	long := strings.Repeat("한글", 5000)
	a := &Agent{
		Provider: &fakeProvider{responses: []Response{
			{Message: AssistantText(long), StopReason: StopEndTurn},
		}},
		Model:  "m",
		Logger: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	if _, err := a.Run(context.Background(), NewSession(), long); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, l := range logLines(t, &buf) {
		for _, field := range []string{"input", "output", "text", "result"} {
			if v, ok := l[field].(string); ok && len(v) > maxLoggedText+8 {
				t.Errorf("field %q is %d bytes, past the bound", field, len(v))
			}
		}
	}
	if buf.Len() > 32<<10 {
		t.Errorf("one run produced %d bytes of log", buf.Len())
	}
}

func TestNoLoggerCostsNothing(t *testing.T) {
	a := &Agent{Provider: &fakeProvider{responses: []Response{
		{Message: AssistantText("ok"), StopReason: StopEndTurn},
	}}, Model: "m"}
	if _, err := a.Run(context.Background(), NewSession(), "go"); err != nil {
		t.Fatalf("a run without a Logger failed: %v", err)
	}
}
