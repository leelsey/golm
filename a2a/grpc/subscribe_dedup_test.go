// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package grpc

import (
	"context"
	"testing"
	"time"

	"github.com/leelsey/golm/a2a"
	pb "github.com/leelsey/golm/a2a/grpc/internal/a2apb"
)

type dedupHandler struct{ release chan struct{} }

func (dedupHandler) HandleMessage(context.Context, a2a.Message) ([]a2a.Part, error) {
	return []a2a.Part{a2a.TextPart("ab")}, nil
}

func (h dedupHandler) HandleMessageStream(_ context.Context, _ a2a.Message, emit func(string) error) ([]a2a.Part, error) {
	_ = emit("a")
	_ = emit("b")
	<-h.release
	return []a2a.Part{a2a.TextPart("ab")}, nil
}

func TestGRPCSubscribeDedupsArtifacts(t *testing.T) {
	h := dedupHandler{release: make(chan struct{})}
	cli, cleanup := dialBuf(t, h)
	defer cleanup()
	ctx := context.Background()

	resp, err := cli.cli.SendMessage(ctx, &pb.SendMessageRequest{
		Message:       &pb.Message{Role: pb.Role_ROLE_USER, Parts: []*pb.Part{textPart("go")}},
		Configuration: &pb.SendMessageConfiguration{ReturnImmediately: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := resp.GetPayload().(*pb.SendMessageResponse_Task).Task.GetId()

	type result struct {
		task a2a.Task
		err  error
	}
	done := make(chan result, 1)
	go func() {
		task, err := cli.SubscribeToTask(ctx, id)
		done <- result{task, err}
	}()

	time.Sleep(50 * time.Millisecond)
	close(h.release)

	r := <-done
	if r.err != nil {
		t.Fatalf("SubscribeToTask: %v", r.err)
	}
	if len(r.task.Artifacts) != 1 {
		t.Fatalf("artifacts = %d, want 1 (deltas + lastChunk deduped)", len(r.task.Artifacts))
	}
	var text string
	for _, p := range r.task.Artifacts[0].Parts {
		text += p.Text
	}
	if text != "ab" {
		t.Errorf("assembled artifact text = %q, want %q", text, "ab")
	}
}
