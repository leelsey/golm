// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/leelsey/golm/internal/jsonrpc"
)

func TestServeParseErrorHasNullID(t *testing.T) {
	var out bytes.Buffer
	tr := NewStreamTransport(strings.NewReader("{ this is not json }\n"), &out, nil)
	if err := NewServer().Serve(context.Background(), tr); err != nil {
		t.Fatalf("serve: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"id":null`)) {
		t.Errorf("parse-error response missing id:null: %s", out.String())
	}
	var resp jsonrpc.Response
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("unmarshal %q: %v", out.String(), err)
	}
	if resp.Error == nil || resp.Error.Code != jsonrpc.CodeParseError {
		t.Errorf("error = %+v, want parse-error code %d", resp.Error, jsonrpc.CodeParseError)
	}
}
