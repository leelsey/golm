// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package hermes_test

import (
	"context"
	"strings"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/provider/hermes"
)

func collect(t *testing.T, chunks []string) (text string, events []golm.StreamEvent, resp golm.Response) {
	t.Helper()
	p := hermes.Wrap(&echoProvider{chunks: [][]string{chunks}})
	var sb strings.Builder
	resp, err := p.Stream(context.Background(), golm.Request{Tools: defs()}, func(ev golm.StreamEvent) error {
		events = append(events, ev)
		if ev.Type == golm.EventTextDelta {
			sb.WriteString(ev.Text)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	return sb.String(), events, resp
}

// A caller streaming to a terminal prints every delta as it arrives.
func TestStreamHidesTagsFromVisibleText(t *testing.T) {
	text, events, resp := collect(t, []string{
		"Let me check. ",
		"<tool_call>{\"name\": \"read_file\", \"arguments\": {\"path\": \"go.mod\"}}</tool_call>",
	})
	if strings.Contains(text, "<tool_call>") || strings.Contains(text, "read_file") {
		t.Errorf("tag text reached the caller: %q", text)
	}
	if !strings.Contains(text, "Let me check") {
		t.Errorf("prose was lost: %q", text)
	}
	var started, stopped int
	for _, ev := range events {
		switch ev.Type {
		case golm.EventToolStart:
			started++
			if ev.ToolName != "read_file" {
				t.Errorf("tool_start named %q", ev.ToolName)
			}
		case golm.EventToolStop:
			stopped++
		}
	}
	if started != 1 || stopped != 1 {
		t.Errorf("tool events: %d start, %d stop; want 1 and 1", started, stopped)
	}
	if resp.StopReason != golm.StopToolUse || len(resp.Message.ToolUses()) != 1 {
		t.Errorf("final response did not carry the call: %q / %d", resp.StopReason, len(resp.Message.ToolUses()))
	}
}

// The whole difficulty: a tag can straddle a chunk boundary.
func TestStreamHandlesTagsSplitAcrossChunks(t *testing.T) {
	whole := "before <tool_call>{\"name\": \"read_file\", \"arguments\": {\"path\": \"x\"}}</tool_call> after"
	for _, size := range []int{1, 2, 3, 5, 7, 13} {
		var chunks []string
		for i := 0; i < len(whole); i += size {
			end := i + size
			if end > len(whole) {
				end = len(whole)
			}
			chunks = append(chunks, whole[i:end])
		}
		text, _, resp := collect(t, chunks)
		if strings.Contains(text, "tool_call") {
			t.Errorf("chunk size %d leaked the tag: %q", size, text)
		}
		if !strings.Contains(text, "before") || !strings.Contains(text, "after") {
			t.Errorf("chunk size %d lost prose: %q", size, text)
		}
		if n := len(resp.Message.ToolUses()); n != 1 {
			t.Errorf("chunk size %d produced %d calls, want 1", size, n)
		}
	}
}

// Text that merely starts like a tag must not be swallowed.
func TestStreamReleasesFalseStarts(t *testing.T) {
	text, _, _ := collect(t, []string{"compare <to", "ol> and <tool", "s> in the docs"})
	if !strings.Contains(text, "<tool> and <tools> in the docs") {
		t.Errorf("text resembling a tag was held back or mangled: %q", text)
	}
}

// A tag left open at the end of a stream was never a tag.
func TestStreamReleasesAnUnclosedTag(t *testing.T) {
	text, _, resp := collect(t, []string{"thinking <tool_call>{\"name\": \"read"})
	if !strings.Contains(text, "thinking") {
		t.Errorf("prose lost: %q", text)
	}
	if !strings.Contains(text, "<tool_call>") {
		t.Errorf("an abandoned tag should be shown, not hidden: %q", text)
	}
	if len(resp.Message.ToolUses()) != 0 {
		t.Error("an unterminated tag became a call")
	}
}

func TestStreamMultipleCalls(t *testing.T) {
	text, events, resp := collect(t, []string{
		"<tool_call>{\"name\": \"read_file\", \"arguments\": {\"path\": \"a\"}}</tool_call>",
		"and\n",
		"<tool_call>{\"name\": \"read_file\", \"arguments\": {\"path\": \"b\"}}</tool_call>",
	})
	uses := resp.Message.ToolUses()
	if len(uses) != 2 {
		t.Fatalf("got %d calls, want 2", len(uses))
	}
	if uses[0].ID == uses[1].ID {
		t.Error("two calls in one turn share an id; a result could not be paired with either")
	}
	if !strings.Contains(text, "and") {
		t.Errorf("prose between calls lost: %q", text)
	}
	var starts int
	for _, ev := range events {
		if ev.Type == golm.EventToolStart {
			starts++
		}
	}
	if starts != 2 {
		t.Errorf("%d tool_start events, want 2", starts)
	}
}

func TestStreamWithoutToolsPassesThrough(t *testing.T) {
	p := hermes.Wrap(&echoProvider{chunks: [][]string{{"plain ", "answer"}}})
	var sb strings.Builder
	resp, err := p.Stream(context.Background(), golm.Request{}, func(ev golm.StreamEvent) error {
		if ev.Type == golm.EventTextDelta {
			sb.WriteString(ev.Text)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if sb.String() != "plain answer" || resp.Message.Text() != "plain answer" {
		t.Errorf("streamed %q / final %q", sb.String(), resp.Message.Text())
	}
}
