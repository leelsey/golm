// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package a2a

import (
	"context"
	"net/http/httptest"
	"testing"
)

type twoChunkHandler struct{}

func (twoChunkHandler) HandleMessage(_ context.Context, _ Message) ([]Part, error) {
	return []Part{TextPart("ab")}, nil
}

func (twoChunkHandler) HandleMessageStream(_ context.Context, _ Message, emit func(string) error) ([]Part, error) {
	if err := emit("a"); err != nil {
		return nil, err
	}
	if err := emit("b"); err != nil {
		return nil, err
	}
	return []Part{TextPart("ab")}, nil
}

func TestStreamFirstChunkEstablishes(t *testing.T) {
	hs := httptest.NewServer(NewServer(CardForAgent("p", "p", "http://x/"), twoChunkHandler{}).HTTPHandler())
	defer hs.Close()

	var deltas []string
	out, err := NewClient(hs.URL).SendMessageStream(context.Background(), "hi", func(s string) {
		deltas = append(deltas, s)
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != "ab" {
		t.Errorf("assembled = %q, want ab (first chunk must not be lost)", out)
	}
	if len(deltas) != 2 || deltas[0] != "a" || deltas[1] != "b" {
		t.Errorf("deltas = %v, want [a b]", deltas)
	}
}
