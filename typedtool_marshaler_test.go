// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"
)

type g3Args struct {
	Blob []byte    `json:"blob"`
	When time.Time `json:"when"`
	IP   net.IP    `json:"ip"`
}

// TestTypedToolMarshalerSchema.
func TestTypedToolMarshalerSchema(t *testing.T) {
	tool := NewTypedTool("g3", "d", func(_ context.Context, _ g3Args) (string, error) { return "", nil })
	var schema struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(tool.Schema(), &schema); err != nil {
		t.Fatalf("schema unmarshal: %v", err)
	}
	for _, f := range []string{"blob", "when", "ip"} {
		if got := schema.Properties[f].Type; got != "string" {
			t.Errorf("property %q type = %q, want string", f, got)
		}
	}
	want := map[string]bool{"blob": true, "when": true, "ip": true}
	for _, r := range schema.Required {
		delete(want, r)
	}
	if len(want) != 0 {
		t.Errorf("missing required entries: %v", want)
	}
}
