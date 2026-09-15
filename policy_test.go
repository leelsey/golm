// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"testing"
)

// Undeclared must never read as harmless.
func TestTraitsOfAnswersUnknownForAnUndeclaredTool(t *testing.T) {
	plain := NewTool("t", "declares nothing", json.RawMessage(`{"type":"object"}`),
		func(context.Context, json.RawMessage) (string, error) { return "", nil })

	if tr, ok := TraitsOf(plain); ok {
		t.Errorf("a plain ToolFunc must not declare traits, got %+v", tr)
	}
	if tr, ok := TraitsOf(nil); ok {
		t.Errorf("a nil Tool must not declare traits, got %+v", tr)
	}
}

func TestWithTraitsDeclaresWithoutChangingTheTool(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"p":{"type":"string"}}}`)
	inner := NewTool("reader", "reads a file", schema,
		func(_ context.Context, input json.RawMessage) (string, error) {
			return "read:" + string(input), nil
		})
	want := ToolTraits{ReadOnly: true, Filesystem: true}
	wrapped := WithTraits(inner, want)

	if got, ok := TraitsOf(wrapped); !ok || got != want {
		t.Fatalf("TraitsOf = %+v, %v; want %+v, true", got, ok, want)
	}
	if wrapped.Name() != "reader" || wrapped.Description() != "reads a file" {
		t.Errorf("identity changed: %q / %q", wrapped.Name(), wrapped.Description())
	}
	if string(wrapped.Schema()) != string(schema) {
		t.Errorf("schema changed: %s", wrapped.Schema())
	}
	out, err := wrapped.Execute(context.Background(), json.RawMessage(`{"p":"x"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := firstText(out); got != `read:{"p":"x"}` {
		t.Errorf("Execute not delegated, got %q", got)
	}

	reg := NewRegistry()
	reg.Register(wrapped)
	got, ok := reg.Get("reader")
	if !ok {
		t.Fatal("a traited tool must register under its own name")
	}
	if tr, ok := TraitsOf(got); !ok || tr != want {
		t.Errorf("traits lost through the Registry: %+v, %v", tr, ok)
	}
}
