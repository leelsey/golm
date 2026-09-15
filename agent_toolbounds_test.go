// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func toolCallThen(final string) []Response {
	return []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "c1", Name: "t", Input: json.RawMessage(`{}`)},
		}}, StopReason: StopToolUse},
		{Message: AssistantText(final), StopReason: StopEndTurn},
	}
}

func TestToolTimeoutMarksTheResult(t *testing.T) {
	reg := NewRegistry()
	reg.Register(NewTool("t", "waits on ctx", json.RawMessage(`{"type":"object"}`),
		func(ctx context.Context, _ json.RawMessage) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		}))

	var got []ToolResult
	a := &Agent{
		Provider: &fakeProvider{responses: toolCallThen("final")},
		Model:    "x", Tools: reg,
		ToolTimeout:  20 * time.Millisecond,
		OnToolResult: func(r ToolResult) { got = append(got, r) },
	}

	res, err := a.Run(context.Background(), NewSession(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Text() != "final" {
		t.Errorf("the loop must continue past a timed-out tool, got %q", res.Text())
	}
	if len(got) != 1 || !got[0].IsError {
		t.Fatalf("want one error result, got %+v", got)
	}
	if res.ToolErrors != 1 {
		t.Errorf("ToolErrors = %d, want 1", res.ToolErrors)
	}
}

func TestMaxToolResultBytesTruncates(t *testing.T) {
	big := strings.Repeat("A", 5000)
	reg := NewRegistry()
	reg.Register(NewTool("t", "returns a lot", json.RawMessage(`{"type":"object"}`),
		func(context.Context, json.RawMessage) (string, error) { return big, nil }))

	var got []ToolResult
	a := &Agent{
		Provider: &fakeProvider{responses: toolCallThen("final")},
		Model:    "x", Tools: reg,
		MaxToolResultBytes: 100,
		OnToolResult:       func(r ToolResult) { got = append(got, r) },
	}
	if _, err := a.Run(context.Background(), NewSession(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want one result, got %d", len(got))
	}
	if got[0].IsError {
		t.Error("truncation is not a tool failure; the tool succeeded")
	}
	text := got[0].Text()
	if !strings.HasPrefix(text, strings.Repeat("A", 100)) {
		t.Error("the kept prefix must be the start of the output")
	}
	if !strings.Contains(text, "truncated") || !strings.Contains(text, "4900") {
		t.Errorf("the cut must be stated with the size omitted, got tail %q", text[len(text)-60:])
	}
	if len(text) > 200 {
		t.Errorf("result still %d bytes; the cap did not apply", len(text))
	}
}

// The hazard this exists for.
func TestDeadlineDoesNotBlockOnAnUncooperativeTool(t *testing.T) {
	started := make(chan struct{})
	unblock := make(chan struct{})
	defer close(unblock)

	reg := NewRegistry()
	reg.Register(NewTool("t", "ignores ctx entirely", json.RawMessage(`{"type":"object"}`),
		func(context.Context, json.RawMessage) (string, error) {
			close(started)
			<-unblock
			return "late", nil
		}))

	a := &Agent{
		Provider: &fakeProvider{responses: toolCallThen("final")},
		Model:    "x", Tools: reg,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	returned := make(chan struct{})
	go func() {
		_, _ = a.Run(ctx, NewSession(), "hi")
		close(returned)
	}()
	<-started

	select {
	case <-returned:
	case <-time.After(3 * time.Second):
		t.Fatal("Run blocked past its deadline on a tool that ignores ctx")
	}
}

// Whatever happens to a tool, its call has to be answered.
func TestAbandonedToolCallIsStillAnswered(t *testing.T) {
	started := make(chan struct{})
	unblock := make(chan struct{})
	defer close(unblock)

	reg := NewRegistry()
	reg.Register(NewTool("t", "ignores ctx entirely", json.RawMessage(`{"type":"object"}`),
		func(context.Context, json.RawMessage) (string, error) {
			close(started)
			<-unblock
			return "late", nil
		}))

	sess := NewSession()
	a := &Agent{
		Provider: &fakeProvider{responses: toolCallThen("final")},
		Model:    "x", Tools: reg,
		ToolTimeout: 20 * time.Millisecond,
	}
	if _, err := a.Run(context.Background(), sess, "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	<-started

	uses, results := 0, 0
	for _, m := range sess.History() {
		uses += len(m.ToolUses())
		for _, c := range m.Content {
			if _, ok := c.(ToolResult); ok {
				results++
			}
		}
	}
	if uses != results {
		t.Fatalf("unbalanced tool_use/tool_result: %d uses, %d results", uses, results)
	}
}

// More hanging calls than MaxParallelTools.
func TestDispatchHonoursTheDeadlineBeyondTheParallelLimit(t *testing.T) {
	unblock := make(chan struct{})
	defer close(unblock)
	reg := NewRegistry()
	reg.Register(NewTool("t", "hangs", json.RawMessage(`{"type":"object"}`),
		func(context.Context, json.RawMessage) (string, error) { <-unblock; return "late", nil }))

	uses := make([]Content, 0, 5)
	for i := 0; i < 5; i++ {
		uses = append(uses, ToolUse{ID: string(rune('a' + i)), Name: "t", Input: json.RawMessage(`{}`)})
	}
	a := &Agent{
		Provider: &fakeProvider{responses: []Response{
			{Message: Message{Role: RoleAssistant, Content: uses}, StopReason: StopToolUse},
			{Message: AssistantText("final"), StopReason: StopEndTurn},
		}},
		Model: "x", Tools: reg,
		ParallelTools: true, MaxParallelTools: 2,
		ToolTimeout: 20 * time.Millisecond,
	}

	sess := NewSession()
	returned := make(chan struct{})
	go func() { _, _ = a.Run(context.Background(), sess, "hi"); close(returned) }()
	select {
	case <-returned:
	case <-time.After(3 * time.Second):
		t.Fatal("Run blocked: dispatch waited for a semaphore slot with no regard for the deadline")
	}

	uses2, results := 0, 0
	for _, m := range sess.History() {
		uses2 += len(m.ToolUses())
		for _, c := range m.Content {
			if _, ok := c.(ToolResult); ok {
				results++
			}
		}
	}
	if uses2 != results {
		t.Fatalf("unbalanced tool_use/tool_result: %d uses, %d results", uses2, results)
	}
}

// Not setting ParallelTools is a promise that tools run one at a time.
func TestSerialToolsStaySerialAfterAnAbandonment(t *testing.T) {
	unblock := make(chan struct{})
	defer close(unblock)
	var live atomic.Int32
	var peak atomic.Int32

	reg := NewRegistry()
	reg.Register(NewTool("t", "hangs", json.RawMessage(`{"type":"object"}`),
		func(context.Context, json.RawMessage) (string, error) {
			n := live.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			defer live.Add(-1)
			<-unblock
			return "late", nil
		}))

	uses := []Content{
		ToolUse{ID: "a", Name: "t", Input: json.RawMessage(`{}`)},
		ToolUse{ID: "b", Name: "t", Input: json.RawMessage(`{}`)},
		ToolUse{ID: "c", Name: "t", Input: json.RawMessage(`{}`)},
	}
	a := &Agent{
		Provider: &fakeProvider{responses: []Response{
			{Message: Message{Role: RoleAssistant, Content: uses}, StopReason: StopToolUse},
			{Message: AssistantText("final"), StopReason: StopEndTurn},
		}},
		Model: "x", Tools: reg,
		ToolTimeout: 20 * time.Millisecond,
	}
	if _, err := a.Run(context.Background(), NewSession(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := peak.Load(); got > 1 {
		t.Errorf("%d tools ran at once without ParallelTools", got)
	}
}

func runBoundedMedia(t *testing.T, limit int, blocks []ToolContent) ToolResult {
	t.Helper()
	reg := NewRegistry()
	reg.Register(NewContentTool("t", "returns media", json.RawMessage(`{"type":"object"}`),
		func(context.Context, json.RawMessage) ([]ToolContent, error) { return blocks, nil }))

	var got []ToolResult
	a := &Agent{
		Provider: &fakeProvider{responses: toolCallThen("final")},
		Model:    "x", Tools: reg,
		MaxToolResultBytes: limit,
		OnToolResult:       func(r ToolResult) { got = append(got, r) },
	}
	if _, err := a.Run(context.Background(), NewSession(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want one result, got %d", len(got))
	}
	return got[0]
}

// Half a PNG is not a smaller PNG.
func TestOverBudgetMediaIsReplacedNotSliced(t *testing.T) {
	png := bytes.Repeat([]byte{0x89}, 5000)
	original := bytes.Clone(png)
	res := runBoundedMedia(t, 100, []ToolContent{Image{MediaType: "image/png", Data: png}})

	for _, c := range res.Content {
		if img, ok := c.(Image); ok {
			t.Fatalf("an over-cap image survived, %d bytes of it", len(img.Data))
		}
	}
	note := res.Text()
	if !strings.Contains(note, "image") || !strings.Contains(note, "5000") {
		t.Errorf("the note must name the kind and the size, got %q", note)
	}
	if !bytes.Equal(png, original) {
		t.Error("the tool's own image data was modified")
	}
	if res.IsError {
		t.Error("dropping media for size is not a tool failure; the tool succeeded")
	}
}

func TestUnderBudgetMediaSurvivesIntact(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a}
	res := runBoundedMedia(t, 1000, []ToolContent{
		Text{Text: "captured"},
		Image{MediaType: "image/png", Data: png},
	})
	if len(res.Content) != 2 {
		t.Fatalf("blocks = %d, want 2 kept whole: %+v", len(res.Content), res.Content)
	}
	if res.Text() != "captured" {
		t.Errorf("text block changed: %q", res.Text())
	}
	img, ok := res.Content[1].(Image)
	if !ok || img.MediaType != "image/png" || !bytes.Equal(img.Data, png) {
		t.Errorf("image block not kept intact: %+v", res.Content[1])
	}
	if res.IsError {
		t.Error("a result inside the cap is not an error")
	}
}

// The cap is on the result, not on each block.
func TestTextAndMediaShareOneBudget(t *testing.T) {
	png := bytes.Repeat([]byte{0x89}, 20)
	res := runBoundedMedia(t, 100, []ToolContent{
		Text{Text: strings.Repeat("A", 90)},
		Image{MediaType: "image/png", Data: png},
	})
	if len(res.Content) != 2 {
		t.Fatalf("blocks = %d, want the text plus a note: %+v", len(res.Content), res.Content)
	}
	if got := res.Content[0].(Text).Text; got != strings.Repeat("A", 90) {
		t.Errorf("the text fitted and must be untouched, got %q", got)
	}
	if _, ok := res.Content[1].(Image); ok {
		t.Fatal("only 10 bytes were left; the 20-byte image must not have fitted")
	}
	if !strings.Contains(res.Content[1].(Text).Text, "image") {
		t.Errorf("the replacement must name the kind, got %q", res.Content[1].(Text).Text)
	}
	if res.IsError {
		t.Error("a bounded result is not an error")
	}
}

// A failing tool's output is re-sent on every remaining step exactly as a successful one is.
func TestToolErrorIsBoundedLikeAResult(t *testing.T) {
	reg := NewRegistry()
	reg.Register(NewTool("t", "fails loudly", json.RawMessage(`{"type":"object"}`),
		func(context.Context, json.RawMessage) (string, error) {
			return "", errors.New(strings.Repeat("x", 5000))
		}))

	var got []ToolResult
	a := &Agent{
		Provider: &fakeProvider{responses: toolCallThen("final")},
		Model:    "x", Tools: reg,
		MaxToolResultBytes: 100,
		OnToolResult:       func(r ToolResult) { got = append(got, r) },
	}
	if _, err := a.Run(context.Background(), NewSession(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got) != 1 || !got[0].IsError {
		t.Fatalf("want one error result, got %+v", got)
	}
	if n := len(got[0].Text()); n > 200 {
		t.Errorf("error result is %d bytes under a 100-byte cap — unbounded on the error path", n)
	}
	if !strings.Contains(got[0].Text(), "truncated") {
		t.Error("truncation was not marked, so the model cannot tell output was cut")
	}
}
