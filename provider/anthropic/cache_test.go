// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package anthropic

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/leelsey/golm"
)

func wire(t *testing.T, req golm.Request) map[string]any {
	t.Helper()
	c := New("k")
	b, err := json.Marshal(c.buildRequest(req, false))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// Caching must be a billing change, not a behavioural one.
func TestCacheDoesNotChangeWhatTheModelReads(t *testing.T) {
	base := golm.Request{
		Model: "claude", MaxTokens: 10, System: golm.SystemPrompt{{Text: "sys"}},
		Messages: []golm.Message{
			golm.UserText("first"),
			golm.AssistantText("second"),
			golm.UserText("third"),
		},
	}
	cached := base
	cached.System = golm.SystemPrompt{{Text: "sys", Cache: true}}
	cached.Cache = golm.CacheConfig{MessagePrefix: 3}

	plain, withCache := wire(t, base), wire(t, cached)

	if plain["system"] != flattenSystem(withCache["system"]) {
		t.Errorf("system text differs:\n plain  %#v\n cached %#v", plain["system"], withCache["system"])
	}

	stripBreakpoints(plain["messages"])
	stripBreakpoints(withCache["messages"])
	if !jsonEq(plain["messages"], withCache["messages"]) {
		t.Errorf("message content differs:\n plain  %s\n cached %s",
			mustJSON(plain["messages"]), mustJSON(withCache["messages"]))
	}

	for _, k := range []string{"model", "max_tokens", "stream", "tools", "tool_choice", "thinking", "temperature"} {
		if !jsonEq(plain[k], withCache[k]) {
			t.Errorf("field %q differs: %#v vs %#v", k, plain[k], withCache[k])
		}
	}
}

func flattenSystem(v any) string {
	blocks, ok := v.([]any)
	if !ok {
		s, _ := v.(string)
		return s
	}
	out := ""
	for _, b := range blocks {
		t, _ := b.(map[string]any)["text"].(string)
		out += t
	}
	return out
}

func stripBreakpoints(v any) {
	msgs, ok := v.([]any)
	if !ok {
		return
	}
	for _, m := range msgs {
		blocks, ok := m.(map[string]any)["content"].([]any)
		if !ok {
			continue
		}
		for _, b := range blocks {
			if bm, ok := b.(map[string]any); ok {
				delete(bm, "cache_control")
			}
		}
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func jsonEq(a, b any) bool { return mustJSON(a) == mustJSON(b) }

func TestCacheOffKeepsSystemAsAString(t *testing.T) {
	got := wire(t, golm.Request{Model: "claude", MaxTokens: 10, System: golm.SystemPrompt{{Text: "sys"}}})
	if s, ok := got["system"].(string); !ok || s != "sys" {
		t.Fatalf("system = %#v, want the plain string \"sys\"", got["system"])
	}
}

func TestCacheSystemEmitsABreakpoint(t *testing.T) {
	got := wire(t, golm.Request{
		Model: "claude", MaxTokens: 10, System: golm.SystemPrompt{{Text: "sys", Cache: true}},
	})
	blocks, ok := got["system"].([]any)
	if !ok || len(blocks) != 1 {
		t.Fatalf("system = %#v, want a one-block array", got["system"])
	}
	b := blocks[0].(map[string]any)
	if b["type"] != "text" || b["text"] != "sys" {
		t.Errorf("block = %#v, want the system text carried through unchanged", b)
	}
	cc, ok := b["cache_control"].(map[string]any)
	if !ok || cc["type"] != "ephemeral" {
		t.Fatalf("cache_control = %#v, want ephemeral", b["cache_control"])
	}

	if _, present := cc["ttl"]; present {
		t.Errorf("default TTL must not emit a ttl field, got %#v", cc)
	}
}

func TestCache1hEmitsTheTTL(t *testing.T) {
	got := wire(t, golm.Request{
		Model: "claude", MaxTokens: 10, System: golm.SystemPrompt{{Text: "sys", Cache: true}},
		Cache: golm.CacheConfig{TTL: golm.CacheTTL1h},
	})
	b := got["system"].([]any)[0].(map[string]any)
	cc := b["cache_control"].(map[string]any)
	if cc["ttl"] != "1h" {
		t.Errorf("ttl = %#v, want \"1h\"", cc["ttl"])
	}
}

func TestCacheSystemMergesSystemRoleMessages(t *testing.T) {
	got := wire(t, golm.Request{
		Model: "claude", MaxTokens: 10, System: golm.SystemPrompt{{Text: "sys", Cache: true}},
		Messages: []golm.Message{
			{Role: golm.RoleSystem, Content: []golm.Content{golm.Text{Text: "more"}}},
			golm.UserText("hi"),
		},
	})
	b := got["system"].([]any)[0].(map[string]any)
	if b["text"] != "sys\n\nmore" {
		t.Errorf("text = %#v, want the merged system prompt", b["text"])
	}
}

func TestCacheSystemWithNoSystemPromptEmitsNothing(t *testing.T) {
	got := wire(t, golm.Request{
		Model: "claude", MaxTokens: 10,
		Messages: []golm.Message{golm.UserText("hi")},
	})
	if v, present := got["system"]; present {
		t.Errorf("system = %#v, want the field omitted entirely", v)
	}
}

func TestCacheMessagePrefixMarksTheFinalBlock(t *testing.T) {
	got := wire(t, golm.Request{
		Model: "claude", MaxTokens: 10,
		Messages: []golm.Message{
			golm.UserText("first"),
			golm.AssistantText("second"),
			golm.UserText("third"),
		},
		Cache: golm.CacheConfig{MessagePrefix: 3},
	})
	msgs := got["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)["content"].([]any)
	b := last[len(last)-1].(map[string]any)
	if _, ok := b["cache_control"]; !ok {
		t.Fatalf("last block = %#v, want a breakpoint", b)
	}

	for i := 0; i < len(msgs)-1; i++ {
		for _, blk := range msgs[i].(map[string]any)["content"].([]any) {
			if _, ok := blk.(map[string]any)["cache_control"]; ok {
				t.Errorf("message %d carries an unexpected breakpoint", i)
			}
		}
	}
}

func TestCacheMessagePrefixStopsShortOfAVaryingTail(t *testing.T) {
	got := wire(t, golm.Request{
		Model: "claude", MaxTokens: 10,
		Messages: []golm.Message{
			golm.UserText("stable one"),
			golm.AssistantText("stable two"),
			golm.UserText("varies every turn"),
		},
		Cache: golm.CacheConfig{MessagePrefix: 2},
	})
	msgs := got["messages"].([]any)
	marked := func(i int) bool {
		blocks := msgs[i].(map[string]any)["content"].([]any)
		_, ok := blocks[len(blocks)-1].(map[string]any)["cache_control"]
		return ok
	}
	if !marked(1) {
		t.Errorf("breakpoint missing from the end of the stable prefix")
	}
	if marked(2) {
		t.Errorf("breakpoint landed on the varying tail")
	}
	if marked(0) {
		t.Errorf("unexpected extra breakpoint on message 0")
	}
}

func TestCacheMessagePrefixCountsTheCallersMessages(t *testing.T) {
	got := wire(t, golm.Request{
		Model: "claude", MaxTokens: 10,
		Messages: []golm.Message{
			{Role: golm.RoleSystem, Content: []golm.Content{golm.Text{Text: "sys"}}},
			golm.UserText("stable"),
			golm.AssistantText("also stable"),
			golm.UserText("varies"),
		},
		Cache: golm.CacheConfig{MessagePrefix: 3},
	})
	msgs := got["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages = %d, want 3 (the system-role entry is folded away)", len(msgs))
	}
	blocks := msgs[1].(map[string]any)["content"].([]any)
	if _, ok := blocks[len(blocks)-1].(map[string]any)["cache_control"]; !ok {
		t.Errorf("breakpoint should sit on \"also stable\", got %#v", msgs)
	}
	tail := msgs[2].(map[string]any)["content"].([]any)
	if _, ok := tail[len(tail)-1].(map[string]any)["cache_control"]; ok {
		t.Errorf("breakpoint landed on the varying tail")
	}
}

func TestCacheMessagePrefixSkipsThinkingBlocks(t *testing.T) {
	got := wire(t, golm.Request{
		Model: "claude", MaxTokens: 10,
		Messages: []golm.Message{{
			Role: golm.RoleAssistant,
			Content: []golm.Content{
				golm.Text{Text: "answer"},
				golm.Thinking{Text: "reasoning"},
			},
		}},
		Cache: golm.CacheConfig{MessagePrefix: 1},
	})
	blocks := got["messages"].([]any)[0].(map[string]any)["content"].([]any)
	think := blocks[len(blocks)-1].(map[string]any)
	if think["type"] != "thinking" {
		t.Fatalf("expected the thinking block last, got %#v", think)
	}
	if _, ok := think["cache_control"]; ok {
		t.Errorf("breakpoint landed on a thinking block: %#v", think)
	}
	text := blocks[0].(map[string]any)
	if _, ok := text["cache_control"]; !ok {
		t.Errorf("breakpoint should have fallen back to the text block: %#v", text)
	}
}

func TestCacheMessagePrefixWithNoMessagesIsSafe(t *testing.T) {
	got := wire(t, golm.Request{
		Model: "claude", MaxTokens: 10, System: golm.SystemPrompt{{Text: "sys", Cache: true}},
		Cache: golm.CacheConfig{MessagePrefix: 4},
	})
	if msgs, ok := got["messages"].([]any); ok && len(msgs) != 0 {
		t.Errorf("messages = %#v, want none", msgs)
	}
}

func TestCacheWhitespaceSystemStaysAPlainString(t *testing.T) {
	for _, mark := range []bool{false, true} {
		got := wire(t, golm.Request{
			Model: "claude", MaxTokens: 10, System: golm.SystemPrompt{{Text: "   ", Cache: mark}},
			Messages: []golm.Message{golm.UserText("hi")},
		})
		if s, ok := got["system"].(string); !ok || s != "   " {
			t.Errorf("Cache=%v: system = %#v, want the plain string", mark, got["system"])
		}
	}
}

func TestToolDefsAreDeterministicallyOrdered(t *testing.T) {
	const rounds = 20
	var want string
	for i := 0; i < rounds; i++ {
		reg := golm.NewRegistry()
		for _, n := range []string{"send_probe", "record_finding", "delegate", "oob_check", "browser_probe"} {
			reg.Register(golm.NewTool(n, "d", json.RawMessage(`{"type":"object"}`),
				func(context.Context, json.RawMessage) (string, error) { return "", nil }))
		}
		got := mustJSON(wire(t, golm.Request{
			Model: "claude", MaxTokens: 10, System: golm.SystemPrompt{{Text: "sys", Cache: true}},
			Tools: reg.Defs(),
		})["tools"])
		if i == 0 {
			want = got
			continue
		}
		if got != want {
			t.Fatalf("tool order is not stable across registries; the cached prefix would differ every call:\n%s\n%s", want, got)
		}
	}
}

func systemMarks(t *testing.T, v any) (texts []string, marked []int) {
	t.Helper()
	blocks, ok := v.([]any)
	if !ok {
		t.Fatalf("system = %#v, want a block array", v)
	}
	for i, b := range blocks {
		bm, ok := b.(map[string]any)
		if !ok || bm["type"] != "text" {
			t.Fatalf("block %d = %#v, want a text block", i, b)
		}
		s, _ := bm["text"].(string)
		texts = append(texts, s)
		if _, ok := bm["cache_control"]; ok {
			marked = append(marked, i)
		}
	}
	return texts, marked
}

func TestCacheSystemSectionsBecomeOneBlockEach(t *testing.T) {
	sys := golm.SystemPrompt{
		{Text: "identity", Cache: true},
		{Text: "tools"},
		{Text: "project", Cache: true},
		{Text: "memory"},
	}
	got := wire(t, golm.Request{Model: "claude", MaxTokens: 10, System: sys})
	texts, marked := systemMarks(t, got["system"])
	if len(texts) != 4 {
		t.Fatalf("blocks = %d, want one per section", len(texts))
	}
	if !jsonEq(marked, []int{0, 2}) {
		t.Errorf("marked blocks = %v, want exactly the marked sections 0 and 2", marked)
	}
	if joined := flattenSystem(got["system"]); joined != sys.Text() {
		t.Errorf("blocks concatenate to %q, want %q", joined, sys.Text())
	}
}

func TestCacheUnmarkedSectionsStayAPlainString(t *testing.T) {
	sys := golm.SystemPrompt{}.Add("identity", "project", "memory")
	got := wire(t, golm.Request{Model: "claude", MaxTokens: 10, System: sys})
	if s, ok := got["system"].(string); !ok || s != sys.Text() {
		t.Fatalf("system = %#v, want the plain string %q", got["system"], sys.Text())
	}
}

func TestCacheSystemRoleFoldsIntoTheLastSection(t *testing.T) {
	sys := golm.SystemPrompt{{Text: "identity", Cache: true}, {Text: "memory"}}
	got := wire(t, golm.Request{
		Model: "claude", MaxTokens: 10, System: sys,
		Messages: []golm.Message{
			{Role: golm.RoleSystem, Content: []golm.Content{golm.Text{Text: "more"}}},
			golm.UserText("hi"),
		},
	})
	texts, marked := systemMarks(t, got["system"])
	if !jsonEq(texts, []string{"identity\n\n", "memory\n\nmore"}) {
		t.Errorf("blocks = %#v, want the fold in the last section", texts)
	}
	if !jsonEq(marked, []int{0}) {
		t.Errorf("marked blocks = %v, want only the stable section", marked)
	}

	if len(sys) != 2 || sys[1].Text != "memory" {
		t.Errorf("the caller's prompt was mutated: %#v", sys)
	}
}

func TestCacheWhitespaceSectionLosesItsMarkNotTheRequest(t *testing.T) {
	sys := golm.SystemPrompt{{Text: "   ", Cache: true}, {Text: "body"}}
	got := wire(t, golm.Request{
		Model: "claude", MaxTokens: 10, System: sys,
		Messages: []golm.Message{golm.UserText("hi")},
	})
	if s, ok := got["system"].(string); !ok || s != sys.Text() {
		t.Fatalf("system = %#v, want the plain string %q", got["system"], sys.Text())
	}
}

func TestCacheBreakpointsAreClampedToFourEarliestFirst(t *testing.T) {
	got := wire(t, golm.Request{
		Model: "claude", MaxTokens: 10,
		System: golm.SystemPrompt{
			{Text: "one", Cache: true},
			{Text: "two", Cache: true},
			{Text: "three", Cache: true},
			{Text: "four", Cache: true},
			{Text: "five", Cache: true},
		},
		Messages: []golm.Message{golm.UserText("hi")},
		Cache:    golm.CacheConfig{MessagePrefix: 1},
	})
	_, marked := systemMarks(t, got["system"])
	if !jsonEq(marked, []int{2, 3, 4}) {
		t.Errorf("marked blocks = %v, want the last three; the earliest marks are the cheap ones to drop", marked)
	}
	total := len(marked)
	for _, m := range got["messages"].([]any) {
		for _, b := range m.(map[string]any)["content"].([]any) {
			if _, ok := b.(map[string]any)["cache_control"]; ok {
				total++
			}
		}
	}
	if total != maxCacheBreakpoints {
		t.Errorf("cache_control count = %d, want %d", total, maxCacheBreakpoints)
	}
}
