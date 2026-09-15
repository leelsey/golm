// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package jsonrpc

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResponsesCoerceNilID(t *testing.T) {
	cases := map[string]*Response{
		"error":  ErrorResponse(nil, CodeParseError, "x"),
		"result": ResultResponse(nil, json.RawMessage(`1`)),
	}
	for name, r := range cases {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("%s: marshal: %v", name, err)
		}
		if !strings.Contains(string(b), `"id":null`) {
			t.Errorf("%s response missing id:null: %s", name, b)
		}
	}
}
