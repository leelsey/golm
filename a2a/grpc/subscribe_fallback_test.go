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

type quickAsync struct{}

func (quickAsync) HandleMessage(_ context.Context, _ a2a.Message) ([]a2a.Part, error) {
	return []a2a.Part{a2a.TextPart("done")}, nil
}

func TestGRPCSubscribeAfterAsyncCompletes(t *testing.T) {
	cli, cleanup := dialBuf(t, quickAsync{})
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

	deadline := time.Now().Add(3 * time.Second)
	for {
		got, err := cli.GetTask(ctx, id)
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		if got.Status.State == a2a.StateCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("task never completed; state=%q", got.Status.State)
		}
		time.Sleep(5 * time.Millisecond)
	}

	task, err := cli.SubscribeToTask(ctx, id)
	if err != nil {
		t.Fatalf("SubscribeToTask: %v", err)
	}
	if task.Status.State != a2a.StateCompleted {
		t.Errorf("fallback subscribe state = %q, want completed", task.Status.State)
	}
}
