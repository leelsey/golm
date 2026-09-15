// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package grpc

import (
	"encoding/json"
	"testing"

	"github.com/leelsey/golm/a2a"
)

func TestToPartsFallsBackOnBadPayload(t *testing.T) {
	parts := toParts([]a2a.Part{
		{Data: json.RawMessage(`{invalid`), Text: "fallback-data"},
		{File: &a2a.FilePart{Bytes: "!!!not-base64"}, Text: "fallback-file"},
		{File: &a2a.FilePart{Name: "report.pdf", MimeType: "application/pdf"}, Text: "fallback-empty"},
	})
	if len(parts) != 3 {
		t.Fatalf("len = %d, want 3 (a bad payload dropped its part)", len(parts))
	}
	if got := parts[0].GetText(); got != "fallback-data" {
		t.Errorf("data part fallback = %q, want fallback-data", got)
	}
	if got := parts[1].GetText(); got != "fallback-file" {
		t.Errorf("file part fallback = %q, want fallback-file", got)
	}
	if got := parts[2].GetText(); got != "fallback-empty" {
		t.Errorf("content-less file part fallback = %q, want fallback-empty", got)
	}
}

func TestToPartsConvertsValidPayloads(t *testing.T) {
	parts := toParts([]a2a.Part{
		{File: &a2a.FilePart{Name: "f.png", MimeType: "image/png", Bytes: "aGk="}},
		{Data: json.RawMessage(`{"k":"v"}`)},
	})
	if len(parts) != 2 {
		t.Fatalf("len = %d, want 2", len(parts))
	}
	if string(parts[0].GetRaw()) != "hi" {
		t.Errorf("file raw = %q, want hi", parts[0].GetRaw())
	}
	if parts[1].GetData() == nil {
		t.Error("data part did not convert to a structpb value")
	}
}
