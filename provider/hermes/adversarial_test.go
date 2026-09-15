// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package hermes_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/provider/hermes"
)

// The scanner holds text back waiting for a tag.
func TestScannerNeverSwallowsText(t *testing.T) {
	cases := map[string]string{
		"bare text":            "just an answer",
		"lone open":            "text <tool_call> and then nothing",
		"lone close":           "text </tool_call> without an open",
		"nested opens":         "<tool_call><tool_call>{}</tool_call>",
		"tag inside prose":     "the <tool_call> tag is how you call a tool",
		"empty call":           "<tool_call></tool_call>",
		"whitespace only":      "<tool_call>   \n  </tool_call>",
		"almost a tag":         "<tool_cal and <tool_calls> and <tool_call",
		"close before open":    "</tool_call>text<tool_call>",
		"unicode around a tag": "앞 <tool_call>{\"name\":\"t\",\"arguments\":{}}</tool_call> 뒤",
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			var chunks []string
			for i := 0; i < len(answer); i++ {
				chunks = append(chunks, answer[i:i+1])
			}
			p := hermes.Wrap(&echoProvider{chunks: [][]string{chunks}})
			var streamed strings.Builder
			resp, err := p.Stream(context.Background(), golm.Request{Tools: defs()},
				func(ev golm.StreamEvent) error {
					if ev.Type == golm.EventTextDelta {
						streamed.WriteString(ev.Text)
					}
					return nil
				})
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}

			for _, word := range strings.Fields(outsideTags(answer)) {
				if !strings.Contains(streamed.String(), word) {
					t.Errorf("the word %q was swallowed\n  wrote:   %q\n  visible: %q",
						word, answer, streamed.String())
				}
			}

			if len(resp.Message.ToolUses()) == 0 {
				for _, word := range strings.Fields(insideTags(answer)) {
					if !strings.Contains(streamed.String(), word) {
						t.Errorf("an unparsed tag body lost %q\n  wrote:   %q\n  visible: %q",
							word, answer, streamed.String())
					}
				}
			}
		})
	}
}

func outsideTags(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "<tool_call>")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		j := strings.Index(s[i:], "</tool_call>")
		if j < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i] + " ")
		s = s[i+j+len("</tool_call>"):]
	}
}

func insideTags(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "<tool_call>")
		if i < 0 {
			return b.String()
		}
		rest := s[i+len("<tool_call>"):]
		j := strings.Index(rest, "</tool_call>")
		if j < 0 {
			return b.String()
		}
		b.WriteString(rest[:j] + " ")
		s = rest[j+len("</tool_call>"):]
	}
}

// Complete and Stream must agree about what was called.
func TestCompleteAndStreamAgree(t *testing.T) {
	answers := []string{
		"plain",
		`before <tool_call>{"name":"read_file","arguments":{"path":"x"}}</tool_call> after`,
		`<tool_call>{"name":"read_file","arguments":{"path":"a"}}</tool_call>` +
			`<tool_call>{"name":"read_file","arguments":{"path":"b"}}</tool_call>`,
		`<tool_call>not json</tool_call>`,
	}
	for _, answer := range answers {
		t.Run(answer[:min(len(answer), 24)], func(t *testing.T) {
			whole, err := hermes.Wrap(&echoProvider{answers: []string{answer}}).
				Complete(context.Background(), golm.Request{Tools: defs()})
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}
			streamed, err := hermes.Wrap(&echoProvider{chunks: [][]string{{answer}}}).
				Stream(context.Background(), golm.Request{Tools: defs()},
					func(golm.StreamEvent) error { return nil })
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			if a, b := len(whole.Message.ToolUses()), len(streamed.Message.ToolUses()); a != b {
				t.Errorf("Complete found %d calls, Stream %d", a, b)
			}
			if whole.StopReason != streamed.StopReason {
				t.Errorf("stop reasons differ: %q vs %q", whole.StopReason, streamed.StopReason)
			}
			if whole.Message.Text() != streamed.Message.Text() {
				t.Errorf("texts differ:\n complete: %q\n stream:   %q",
					whole.Message.Text(), streamed.Message.Text())
			}
		})
	}
}

// A call id has to be unique within a turn, or a result cannot be paired with the call it answers.
func TestCallIDsAreUniqueWithinATurn(t *testing.T) {
	var answer strings.Builder
	for i := 0; i < 5; i++ {
		answer.WriteString(`<tool_call>{"name":"read_file","arguments":{"path":"x"}}</tool_call>`)
	}
	resp, err := hermes.Wrap(&echoProvider{answers: []string{answer.String()}}).
		Complete(context.Background(), golm.Request{Tools: defs()})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	seen := map[string]bool{}
	for _, u := range resp.Message.ToolUses() {
		if seen[u.ID] {
			t.Errorf("duplicate call id %q", u.ID)
		}
		seen[u.ID] = true
	}
	if len(seen) != 5 {
		t.Errorf("%d distinct ids for 5 calls", len(seen))
	}
}

// Arguments must always reach a tool as a JSON OBJECT, whatever the model produced.
func TestArgumentsAreAlwaysAnObject(t *testing.T) {
	for _, args := range []string{
		`{"path":"x"}`, `"{\"path\":\"x\"}"`, `null`, `[]`, `"plain string"`, `42`,
	} {
		answer := `<tool_call>{"name":"read_file","arguments":` + args + `}</tool_call>`
		resp, err := hermes.Wrap(&echoProvider{answers: []string{answer}}).
			Complete(context.Background(), golm.Request{Tools: defs()})
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		uses := resp.Message.ToolUses()
		if len(uses) != 1 {
			t.Errorf("arguments %s produced %d calls", args, len(uses))
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal(uses[0].Input, &obj); err != nil {
			t.Errorf("arguments %s reached the tool as %s, which is not an object", args, uses[0].Input)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type turnProvider struct{ text string }

func (t *turnProvider) Name() string                    { return "turn" }
func (t *turnProvider) Capabilities() golm.Capabilities { return golm.Capabilities{} }
func (t *turnProvider) Complete(context.Context, golm.Request) (golm.Response, error) {
	return golm.Response{Message: golm.AssistantText(t.text), StopReason: golm.StopEndTurn}, nil
}
func (t *turnProvider) Stream(ctx context.Context, r golm.Request, fn func(golm.StreamEvent) error) (golm.Response, error) {
	for _, chunk := range strings.Split(t.text, "\n") {
		if err := fn(golm.StreamEvent{Type: golm.EventTextDelta, Text: chunk + "\n"}); err != nil {
			return golm.Response{}, err
		}
	}
	return golm.Response{StopReason: golm.StopEndTurn}, nil
}

// The convention carries no call id.
func TestTagIDsAreDistinctAcrossTurns(t *testing.T) {
	const body = "<tool_call>\n{\"name\": \"probe\", \"arguments\": {}}\n</tool_call>"
	for _, mode := range []string{"complete", "stream"} {
		t.Run(mode, func(t *testing.T) {
			p := hermes.Wrap(&turnProvider{text: body})
			req := golm.Request{Tools: []golm.ToolDef{{Name: "probe", Description: "d"}}}
			seen := map[string]bool{}
			for turn := 0; turn < 3; turn++ {
				req.Messages = append(req.Messages, golm.UserText("go"))
				var resp golm.Response
				var err error
				if mode == "stream" {
					resp, err = p.Stream(context.Background(), req, func(golm.StreamEvent) error { return nil })
				} else {
					resp, err = p.Complete(context.Background(), req)
				}
				if err != nil {
					t.Fatalf("turn %d: %v", turn, err)
				}
				for _, c := range resp.Message.Content {
					u, ok := c.(golm.ToolUse)
					if !ok {
						continue
					}
					if seen[u.ID] {
						t.Errorf("turn %d reused tool call id %q", turn, u.ID)
					}
					seen[u.ID] = true
				}
				req.Messages = append(req.Messages, resp.Message)
			}
			if len(seen) != 3 {
				t.Errorf("%d distinct ids across three turns, want 3", len(seen))
			}
		})
	}
}

// The streaming path stamps ids while scanning and interpret re-parses the same text afterwards.
func TestStreamAndInterpretAgreeOnIDs(t *testing.T) {
	const body = "<tool_call>\n{\"name\": \"probe\", \"arguments\": {}}\n</tool_call>"
	p := hermes.Wrap(&turnProvider{text: body})
	req := golm.Request{
		Messages: []golm.Message{golm.UserText("a"), golm.AssistantText("b"), golm.UserText("c")},
		Tools:    []golm.ToolDef{{Name: "probe", Description: "d"}},
	}
	var streamed []string
	resp, err := p.Stream(context.Background(), req, func(ev golm.StreamEvent) error {
		if ev.Type == golm.EventToolStart {
			streamed = append(streamed, ev.ToolID)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var final []string
	for _, c := range resp.Message.Content {
		if u, ok := c.(golm.ToolUse); ok {
			final = append(final, u.ID)
		}
	}
	if len(final) == 0 {
		t.Fatal("no tool call was parsed")
	}
	if !slices.Equal(streamed, final) {
		t.Errorf("stream announced %v, message holds %v", streamed, final)
	}
}
