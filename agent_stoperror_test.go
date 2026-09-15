// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// A refusal can arrive after real work.
func TestStopErrorCarriesTheResponse(t *testing.T) {
	fp := &fakeProvider{responses: []Response{
		{
			Message:    Message{Role: RoleAssistant, Content: []Content{Text{Text: "partial"}}},
			StopReason: StopRefusal,
			Usage:      Usage{InputTokens: 120, OutputTokens: 7},
		},
	}}
	a := &Agent{Provider: fp, Model: "x"}

	res, err := a.Run(context.Background(), NewSession(), "hi")
	var se *StopError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want a *StopError", err)
	}
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("err does not unwrap to ErrRefused: %v", err)
	}
	if se.StopReason != StopRefusal {
		t.Errorf("StopError.StopReason = %q, want refusal", se.StopReason)
	}
	if se.Response.Usage.Total() != 127 {
		t.Errorf("StopError.Response.Usage.Total() = %d, want 127 — a refusal is billed", se.Response.Usage.Total())
	}
	if res.Text() != "partial" {
		t.Errorf("Run dropped the message on the error path: %q", res.Text())
	}
	if res.Usage.Total() != 127 {
		t.Errorf("Result.Usage = %d, want 127", res.Usage.Total())
	}
}

func TestStopErrorDistinguishesOverflowFromRefusal(t *testing.T) {
	for _, tc := range []struct {
		name    string
		stop    StopReason
		want    error
		notWant error
	}{
		{"refusal", StopRefusal, ErrRefused, ErrContextOverflow},
		{"overflow", StopContextOverflow, ErrContextOverflow, ErrRefused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fp := &fakeProvider{responses: []Response{{Message: AssistantText(""), StopReason: tc.stop}}}
			a := &Agent{Provider: fp, Model: "x"}
			_, err := a.Run(context.Background(), NewSession(), "hi")
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if errors.Is(err, tc.notWant) {
				t.Fatalf("err = %v must not also match %v", err, tc.notWant)
			}
		})
	}
}

// StopOther stays a nil-error return.
func TestStopOtherIsNotAnError(t *testing.T) {
	fp := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "c1", Name: "echo", Input: json.RawMessage(`{}`)},
		}}, StopReason: StopOther},
	}}
	reg := NewRegistry()
	reg.Register(echoTool())
	a := &Agent{Provider: fp, Model: "x", Tools: reg}

	res, err := a.Run(context.Background(), NewSession(), "hi")
	if err != nil {
		t.Fatalf("StopOther must not be an error, got %v", err)
	}
	if res.StopReason != StopOther {
		t.Fatalf("StopReason = %q, want other", res.StopReason)
	}
}
