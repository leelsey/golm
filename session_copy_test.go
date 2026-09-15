// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestHistoryContentIsCopied(t *testing.T) {
	s := NewSession()
	s.Append(UserText("secret"))

	hist := s.History()
	hist[0].Content[0] = Text{Text: "[redacted]"}

	if got := s.History()[0].Text(); got != "secret" {
		t.Fatalf("session history = %q after mutating a returned copy, want %q", got, "secret")
	}
}

func mediaMessage() Message {
	return Message{Role: RoleUser, Content: []Content{
		Image{MediaType: "image/png", Data: []byte{1, 2, 3}},
		Audio{MediaType: "audio/wav", Data: []byte{4, 5, 6}},
		ToolUse{ID: "t1", Name: "echo", Input: json.RawMessage(`{"a":1}`)},
	}}
}

func checkPayloads(t *testing.T, m Message, wantImage, wantAudio []byte, wantInput string) {
	t.Helper()
	if got := m.Content[0].(Image).Data; !bytes.Equal(got, wantImage) {
		t.Errorf("Image.Data = %v, want %v", got, wantImage)
	}
	if got := m.Content[1].(Audio).Data; !bytes.Equal(got, wantAudio) {
		t.Errorf("Audio.Data = %v, want %v", got, wantAudio)
	}
	if got := string(m.Content[2].(ToolUse).Input); got != wantInput {
		t.Errorf("ToolUse.Input = %s, want %s", got, wantInput)
	}
}

// Egress: a caller holding a History result cannot rewrite the transcript through the byte payloads it shares.
func TestHistoryPayloadsAreDeepCopied(t *testing.T) {
	s := NewSession()
	s.Append(mediaMessage())

	hist := s.History()
	hist[0].Content[0].(Image).Data[0] = 0xFF
	hist[0].Content[1].(Audio).Data[0] = 0xFF
	hist[0].Content[2].(ToolUse).Input[1] = 'z'

	checkPayloads(t, s.History()[0], []byte{1, 2, 3}, []byte{4, 5, 6}, `{"a":1}`)
}

// Ingress: a caller reusing the buffers it built a message from cannot rewrite the transcript after handing the message to Append.
func TestAppendPayloadsAreDeepCopied(t *testing.T) {
	s := NewSession()
	msg := mediaMessage()
	s.Append(msg)

	msg.Content[0].(Image).Data[0] = 0xFF
	msg.Content[1].(Audio).Data[0] = 0xFF
	msg.Content[2].(ToolUse).Input[1] = 'z'

	checkPayloads(t, s.History()[0], []byte{1, 2, 3}, []byte{4, 5, 6}, `{"a":1}`)
}

func TestZeroValueRegistryRegister(t *testing.T) {
	var r Registry
	r.Register(echoTool())
	if _, ok := r.Get("echo"); !ok {
		t.Fatal("tool not registered on zero-value Registry")
	}
}

func TestZeroValueSessionState(t *testing.T) {
	var s Session
	if err := s.SetState("k", 1); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	v, ok, err := GetState[int](&s, "k")
	if err != nil || !ok || v != 1 {
		t.Fatalf("GetState(k) = %v, %v, %v; want 1, true, nil", v, ok, err)
	}
}
