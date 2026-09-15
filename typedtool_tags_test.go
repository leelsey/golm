// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"testing"
)

type embOpt struct {
	A string `json:"a"`
}

type embOptArgs struct {
	embOpt `json:",omitempty"`
	B      string `json:"b"`
}

type dashArgs struct {
	//lint:ignore SA5008 deliberate: a field named "-", which is what is under test
	X int `json:"-,"`
	Y int `json:"y"`
}

func schemaProps(t *testing.T, tool Tool) map[string]any {
	t.Helper()
	var s map[string]any
	if err := json.Unmarshal(tool.Schema(), &s); err != nil {
		t.Fatalf("schema: %v", err)
	}
	p, _ := s["properties"].(map[string]any)
	return p
}

func TestNewTypedToolTagEdgeCases(t *testing.T) {
	embTool := NewTypedTool("emb", "", func(_ context.Context, _ embOptArgs) (string, error) { return "", nil })
	p := schemaProps(t, embTool)
	if _, ok := p["a"]; !ok {
		t.Error("embedded field 'a' should be promoted to top level")
	}
	if _, ok := p["b"]; !ok {
		t.Error("'b' should be present")
	}
	if _, ok := p["embOpt"]; ok {
		t.Error("embedded struct must not be nested under its type name")
	}

	dashTool := NewTypedTool("dash", "", func(_ context.Context, _ dashArgs) (string, error) { return "", nil })
	p = schemaProps(t, dashTool)
	if _, ok := p["-"]; !ok {
		t.Error(`field tagged json:"-," (named "-") must appear in the schema`)
	}
	if _, ok := p["y"]; !ok {
		t.Error("'y' should be present")
	}
}
