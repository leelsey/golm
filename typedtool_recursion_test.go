// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"testing"
)

type treeNode struct {
	Val      string     `json:"val"`
	Children []treeNode `json:"children,omitempty"`
}

func TestNewTypedToolRecursiveTypeNoOverflow(t *testing.T) {
	tool := NewTypedTool("tree", "walk a tree", func(_ context.Context, _ treeNode) (string, error) {
		return "ok", nil
	})
	var schema map[string]any
	if err := json.Unmarshal(tool.Schema(), &schema); err != nil {
		t.Fatalf("schema not valid JSON: %v", err)
	}
	props := schema["properties"].(map[string]any)
	children := props["children"].(map[string]any)
	if children["type"] != "array" {
		t.Fatalf("children not an array: %v", children)
	}

	items, _ := children["items"].(map[string]any)
	if _, hasType := items["type"]; hasType {
		t.Errorf("recursive items should degrade to {}, got %v", items)
	}
}

type embBase struct {
	ID string `json:"id"`
}

type embArgs struct {
	embBase
	Name string `json:"name"`
}

func TestNewTypedToolEmbeddedFieldsPromoted(t *testing.T) {
	var got embArgs
	tool := NewTypedTool("emb", "", func(_ context.Context, in embArgs) (string, error) {
		got = in
		return "ok", nil
	})
	var schema map[string]any
	json.Unmarshal(tool.Schema(), &schema)
	props := schema["properties"].(map[string]any)
	if _, ok := props["id"]; !ok {
		t.Error("embedded field 'id' should be promoted to a top-level property")
	}
	if _, ok := props["name"]; !ok {
		t.Error("'name' should be a property")
	}
	if _, ok := props["embBase"]; ok {
		t.Error("embedded struct must not appear nested under its type name")
	}

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"id":"x","name":"y"}`)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got.ID != "x" || got.Name != "y" {
		t.Fatalf("decoded wrong: %+v", got)
	}
}
