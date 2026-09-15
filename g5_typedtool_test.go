// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"testing"
)

func TestNewTypedToolSchemaAndDecode(t *testing.T) {
	type args struct {
		Query string `json:"query"`
		Limit int    `json:"limit,omitempty"`
		Tags  []string
	}
	var got args
	tool := NewTypedTool("search", "search things", func(_ context.Context, in args) (string, error) {
		got = in
		return "ok", nil
	})

	var schema map[string]any
	if err := json.Unmarshal(tool.Schema(), &schema); err != nil {
		t.Fatalf("schema not valid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Fatalf("schema type = %v, want object", schema["type"])
	}
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["query"]; !ok {
		t.Error("schema missing 'query' property")
	}

	req, _ := schema["required"].([]any)
	hasQuery := false
	for _, r := range req {
		if r == "query" {
			hasQuery = true
		}
		if r == "limit" {
			t.Error("limit should not be required (omitempty)")
		}
	}
	if !hasQuery {
		t.Error("query should be required")
	}

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"go","limit":5,"Tags":["a"]}`))
	if err != nil || firstText(out) != "ok" {
		t.Fatalf("execute: out=%q err=%v", firstText(out), err)
	}
	if got.Query != "go" || got.Limit != 5 || len(got.Tags) != 1 {
		t.Fatalf("decoded wrong: %+v", got)
	}
}

func TestNewAgentAndValidate(t *testing.T) {
	if err := (&Agent{}).Validate(); err == nil {
		t.Error("no provider: want error")
	}
	if err := (&Agent{Provider: &fakeProvider{}}).Validate(); err == nil {
		t.Error("no model: want error")
	}
	a := NewAgent(&fakeProvider{}, "m")
	if err := a.Validate(); err != nil {
		t.Errorf("NewAgent should be valid: %v", err)
	}
}
