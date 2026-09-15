// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TextTool's schema says its argument is required.
func TestTextToolEnforcesItsRequiredArgument(t *testing.T) {
	var got string
	var ran int
	tool := TextTool("delegate", "d", "task", "the task", func(_ context.Context, s string) (string, error) {
		ran++
		got = s
		return "ok", nil
	})

	for _, tc := range []struct {
		name, args, wantErr string
	}{
		{"correct", `{"task":"do the thing"}`, ""},
		{"wrong name", `{"tasks":["do the thing"]}`, `missing required argument "task"`},
		{"absent", `{}`, `missing required argument "task"`},
		{"null", `{"task":null}`, `must be a string`},
		{"not a string", `{"task":["a"]}`, `must be a string`},
		{"empty", `{"task":""}`, `must not be empty`},
		{"blank", `{"task":"  \n "}`, `must not be empty`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := ran
			_, err := tool.Execute(context.Background(), json.RawMessage(tc.args))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Execute: %v", err)
				}
				if got != "do the thing" {
					t.Errorf("tool received %q", got)
				}
				return
			}
			if err == nil {
				t.Fatalf("no error; the tool ran on %q", got)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want it to mention %q", err, tc.wantErr)
			}
			if ran != before {
				t.Error("the tool body ran despite the bad arguments")
			}
		})
	}
}

// The schema and the check must agree, or one of them is a lie.
func TestTextToolSchemaMatchesWhatItEnforces(t *testing.T) {
	tool := TextTool("t", "d", "task", "", nil)
	var schema struct {
		Required   []string                  `json:"required"`
		Properties map[string]map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(tool.Schema(), &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "task" {
		t.Errorf("required = %v, want [task]", schema.Required)
	}
	if schema.Properties["task"]["type"] != "string" {
		t.Errorf("task type = %v, want string", schema.Properties["task"]["type"])
	}
}
