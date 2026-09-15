// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"log/slog"
)

const maxLoggedText = 512

func (a *Agent) log(ctx context.Context, kind string, data any) {
	if a.Logger == nil {
		return
	}
	attrs := []any{"agent", a.Name, "event", kind}
	level := slog.LevelDebug
	msg := "agent " + kind

	switch v := data.(type) {
	case StepEvent:
		attrs = append(attrs,
			"step", v.Step, "stop_reason", string(v.StopReason),
			"input_tokens", v.Usage.InputTokens, "output_tokens", v.Usage.OutputTokens,
			"total_tokens", v.Usage.Total())
		if v.Usage.CacheReadTokens > 0 {
			attrs = append(attrs, "cache_read_tokens", v.Usage.CacheReadTokens)
		}
	case ToolCallEvent:
		attrs = append(attrs, "tool", v.Name, "call_id", v.ID, "input", truncRunes(string(v.Input), maxLoggedText))
	case ToolResultEvent:
		attrs = append(attrs, "tool", v.Name, "call_id", v.ID, "is_error", v.IsError,
			"result", truncRunes(v.Content, maxLoggedText))
		if v.IsError {
			level, msg = slog.LevelWarn, "agent tool failed"
		}
	case string:
		switch kind {
		case "error":
			level, msg = slog.LevelError, "agent run failed"
			attrs = append(attrs, "error", truncRunes(v, maxLoggedText))
		case "start":
			level, msg = slog.LevelInfo, "agent run started"
			attrs = append(attrs, "input", truncRunes(v, maxLoggedText))
		case "done":
			level, msg = slog.LevelInfo, "agent run finished"
			attrs = append(attrs, "output", truncRunes(v, maxLoggedText))
		default:
			attrs = append(attrs, "text", truncRunes(v, maxLoggedText))
		}
	}
	a.Logger.Log(ctx, level, msg, attrs...)
}
