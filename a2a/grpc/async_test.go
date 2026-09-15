// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package grpc

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/leelsey/golm/a2a"
	pb "github.com/leelsey/golm/a2a/grpc/internal/a2apb"
)

type canceledHandler struct{}

func (canceledHandler) HandleMessage(_ context.Context, _ a2a.Message) ([]a2a.Part, error) {
	return nil, context.Canceled
}

func TestGRPCSendMessageCanceledTyping(t *testing.T) {
	cli, cleanup := dialBuf(t, canceledHandler{})
	defer cleanup()
	if _, err := cli.SendMessage(context.Background(), "hi"); status.Code(err) != codes.Canceled {
		t.Errorf("SendMessage error code = %v, want Canceled", status.Code(err))
	}
}

type liveStream struct {
	started chan struct{}
	release chan struct{}
}

func (liveStream) HandleMessage(_ context.Context, _ a2a.Message) ([]a2a.Part, error) {
	return []a2a.Part{a2a.TextPart("x")}, nil
}

func (h *liveStream) HandleMessageStream(_ context.Context, _ a2a.Message, emit func(string) error) ([]a2a.Part, error) {
	close(h.started)
	_ = emit("x")
	<-h.release
	return []a2a.Part{a2a.TextPart("x")}, nil
}

func TestGRPCLiveSubscribe(t *testing.T) {
	h := &liveStream{started: make(chan struct{}), release: make(chan struct{})}
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
	task := resp.GetPayload().(*pb.SendMessageResponse_Task).Task
	if task.GetStatus().GetState() != pb.TaskState_TASK_STATE_WORKING {
		t.Errorf("async task state = %v, want WORKING", task.GetStatus().GetState())
	}
	<-h.started

	stream, err := cli.cli.SubscribeToTask(ctx, &pb.SubscribeToTaskRequest{Id: task.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		states    []pb.TaskState
		artifacts []string
	}
	done := make(chan result, 1)
	go func() {
		var r result
		for {
			ev, rerr := stream.Recv()
			if rerr != nil {
				break
			}
			switch p := ev.GetPayload().(type) {
			case *pb.StreamResponse_StatusUpdate:
				r.states = append(r.states, p.StatusUpdate.GetStatus().GetState())
			case *pb.StreamResponse_ArtifactUpdate:
				r.artifacts = append(r.artifacts, artifactText(p.ArtifactUpdate.GetArtifact()))
			}
		}
		done <- r
	}()

	close(h.release)
	select {
	case r := <-done:
		sawCompleted := false
		for _, st := range r.states {
			if st == pb.TaskState_TASK_STATE_COMPLETED {
				sawCompleted = true
			}
		}
		if !sawCompleted {
			t.Errorf("subscriber states = %v, want a COMPLETED", r.states)
		}
		sawX := false
		for _, a := range r.artifacts {
			if a == "x" {
				sawX = true
			}
		}
		if !sawX {
			t.Errorf("subscriber artifacts = %v, want an x", r.artifacts)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("subscribe stream did not complete")
	}
}
