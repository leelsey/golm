// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package grpc

import (
	"context"
	"testing"

	"github.com/leelsey/golm/a2a"
)

type plainHandler struct{}

func (plainHandler) HandleMessage(_ context.Context, msg a2a.Message) ([]a2a.Part, error) {
	return []a2a.Part{a2a.TextPart("plain:" + msg.Text())}, nil
}

func TestGRPCStreamFallbackNonStreamingHandler(t *testing.T) {
	cli, cleanup := dialBuf(t, plainHandler{})
	defer cleanup()
	out, err := cli.SendMessageStream(context.Background(), "hi", nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if out != "plain:hi" {
		t.Errorf("non-streaming handler over gRPC stream = %q, want plain:hi", out)
	}
}
