// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package hermes

import (
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

// parseCalls reads MODEL output.
func FuzzParseCalls(f *testing.F) {
	f.Add("plain text")
	f.Add("<tool_call>{\"name\":\"f\",\"arguments\":{}}</tool_call>")
	f.Add("<tool_call>not json</tool_call>")
	f.Add("<tool_call>")
	f.Add("</tool_call>")
	f.Add("<tool_call><tool_call>{}</tool_call>")
	f.Add("<tool_call>{\"name\":\"\",\"arguments\":null}</tool_call>")
	f.Add("a<tool_call>{\"name\":\"f\",\"arguments\":\"{}\"}</tool_call>b")
	f.Add(strings.Repeat("<tool_call>", 64))

	f.Fuzz(func(t *testing.T, s string) {
		content, n := parseCalls(s, 0)
		if n < 0 {
			t.Fatalf("negative call count %d", n)
		}
		var calls int
		for _, c := range content {
			switch v := c.(type) {
			case golm.ToolUse:
				calls++
				if v.Name == "" {
					t.Fatalf("a tool call with no name was accepted from %q", s)
				}
				if len(v.Input) == 0 || !isJSONObject(v.Input) {
					t.Fatalf("tool %q got arguments that are not a JSON object: %q", v.Name, v.Input)
				}
			case golm.Text:

			default:
				t.Fatalf("parseCalls produced %T, which the agent loop cannot carry", c)
			}
		}
		if calls != n {
			t.Fatalf("reported %d calls but produced %d", n, calls)
		}
	})
}

func isJSONObject(b []byte) bool {
	s := strings.TrimSpace(string(b))
	return strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}")
}

// The streaming scanner sees the same text arbitrarily split.
func FuzzScannerNeverLosesText(f *testing.F) {
	f.Add("hello <tool_call>{\"name\":\"f\",\"arguments\":{}}</tool_call> bye", 3)
	f.Add("<tool_c", 1)
	f.Add("<tool_call>unterminated", 5)
	f.Add("no tags at all", 2)

	f.Fuzz(func(t *testing.T, s string, chunk int) {
		if chunk <= 0 {
			chunk = 1
		}
		if chunk > 64 {
			chunk = 64
		}
		var visible strings.Builder
		var calls int
		sc := &scanner{emit: func(ev golm.StreamEvent) error {
			switch ev.Type {
			case golm.EventTextDelta:
				visible.WriteString(ev.Text)
			case golm.EventToolStart:
				calls++
			}
			return nil
		}}
		for i := 0; i < len(s); i += chunk {
			j := min(i+chunk, len(s))
			if err := sc.feed(s[i:j]); err != nil {
				t.Fatalf("feed: %v", err)
			}
		}
		if err := sc.finish(); err != nil {
			t.Fatalf("finish: %v", err)
		}

		if calls == 0 && visible.String() != s {
			t.Fatalf("text changed with no call parsed:\n  in  %q\n  out %q", s, visible.String())
		}
		if len(visible.String()) > len(s) {
			t.Fatalf("scanner produced more text than it was given: %d > %d", len(visible.String()), len(s))
		}
	})
}
