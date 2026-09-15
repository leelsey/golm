// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package anthropic

import (
	"context"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

func TestAudioContentRejected(t *testing.T) {
	c := New("k").WithBaseURL("http://127.0.0.1:1")
	req := golm.Request{Model: "m", Messages: []golm.Message{
		{Role: golm.RoleUser, Content: []golm.Content{golm.Audio{MediaType: "audio/wav", Data: []byte{1}}}},
	}}
	if _, err := c.Complete(context.Background(), req); err == nil || !strings.Contains(err.Error(), "audio") {
		t.Fatalf("Complete err = %v, want an audio-not-supported error", err)
	}
	_, err := c.Stream(context.Background(), req, func(golm.StreamEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "audio") {
		t.Fatalf("Stream err = %v, want an audio-not-supported error", err)
	}
}
