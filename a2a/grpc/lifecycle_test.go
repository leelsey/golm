// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package grpc

import (
	"context"
	"errors"
	"io"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/a2a"
	pb "github.com/leelsey/golm/a2a/grpc/internal/a2apb"
)

func stubHandler() a2a.Handler {
	return a2a.AgentHandler(&golm.Agent{Provider: stubProvider{}, Model: "x"})
}

func TestGRPCGetTask(t *testing.T) {
	cli, cleanup := dialBuf(t, stubHandler())
	defer cleanup()
	if _, err := cli.SendMessage(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	task, err := cli.GetTask(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status.State != a2a.StateCompleted {
		t.Errorf("state = %q, want completed", task.Status.State)
	}
	if len(task.Artifacts) == 0 || len(task.Artifacts[0].Parts) == 0 || task.Artifacts[0].Parts[0].Text != "agent:hi" {
		t.Errorf("artifact = %+v, want agent:hi", task.Artifacts)
	}
}

func TestGRPCGetTaskNotFound(t *testing.T) {
	cli, cleanup := dialBuf(t, stubHandler())
	defer cleanup()
	if _, err := cli.GetTask(context.Background(), "nope"); status.Code(err) != codes.NotFound {
		t.Errorf("GetTask(nope) code = %v, want NotFound", status.Code(err))
	}
}

func TestGRPCCancelTerminal(t *testing.T) {
	cli, cleanup := dialBuf(t, stubHandler())
	defer cleanup()
	if _, err := cli.SendMessage(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.CancelTask(context.Background(), "task-1"); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("Cancel(terminal) code = %v, want FailedPrecondition", status.Code(err))
	}
	task, err := cli.GetTask(context.Background(), "task-1")
	if err != nil || task.Status.State != a2a.StateCompleted {
		t.Errorf("task should remain completed; got state=%v err=%v", task.Status.State, err)
	}
}

func TestGRPCCancelNotFound(t *testing.T) {
	cli, cleanup := dialBuf(t, stubHandler())
	defer cleanup()
	if _, err := cli.CancelTask(context.Background(), "nope"); status.Code(err) != codes.NotFound {
		t.Errorf("Cancel(nope) code = %v, want NotFound", status.Code(err))
	}
}

func TestGRPCListTasks(t *testing.T) {
	cli, cleanup := dialBuf(t, stubHandler())
	defer cleanup()
	if _, err := cli.SendMessage(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.SendMessage(context.Background(), "b"); err != nil {
		t.Fatal(err)
	}
	all, err := cli.ListTasks(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Errorf("ListTasks(all) = %d, want 2", len(all))
	}
	if done, _ := cli.ListTasks(context.Background(), "", a2a.StateCompleted); len(done) != 2 {
		t.Errorf("ListTasks(completed) = %d, want 2", len(done))
	}
	if working, _ := cli.ListTasks(context.Background(), "", a2a.StateWorking); len(working) != 0 {
		t.Errorf("ListTasks(working) = %d, want 0", len(working))
	}
}

func TestGRPCSubscribeTerminal(t *testing.T) {
	cli, cleanup := dialBuf(t, stubHandler())
	defer cleanup()
	if _, err := cli.SendMessage(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	task, err := cli.SubscribeToTask(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if task.Status.State != a2a.StateCompleted {
		t.Errorf("state = %q, want completed", task.Status.State)
	}
	if len(task.Artifacts) == 0 {
		t.Error("expected an artifact in the subscribe result")
	}
}

func TestGRPCSubscribeNotFound(t *testing.T) {
	cli, cleanup := dialBuf(t, stubHandler())
	defer cleanup()
	if _, err := cli.SubscribeToTask(context.Background(), "nope"); status.Code(err) != codes.NotFound {
		t.Errorf("Subscribe(nope) code = %v, want NotFound", status.Code(err))
	}
}

type twoChunkStream struct{}

func (twoChunkStream) HandleMessage(_ context.Context, _ a2a.Message) ([]a2a.Part, error) {
	return []a2a.Part{a2a.TextPart("ab")}, nil
}

func (twoChunkStream) HandleMessageStream(_ context.Context, _ a2a.Message, emit func(string) error) ([]a2a.Part, error) {
	if err := emit("a"); err != nil {
		return nil, err
	}
	if err := emit("b"); err != nil {
		return nil, err
	}
	return []a2a.Part{a2a.TextPart("ab")}, nil
}

func TestGRPCStreamEvents(t *testing.T) {
	cli, cleanup := dialBuf(t, twoChunkStream{})
	defer cleanup()
	stream, err := cli.cli.SendStreamingMessage(context.Background(), userRequest("hi"))
	if err != nil {
		t.Fatal(err)
	}
	type ev struct {
		kind  string
		state pb.TaskState
		text  string
		app   bool
		last  bool
	}
	var evs []ev
	for {
		resp, rerr := stream.Recv()
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			t.Fatal(rerr)
		}
		switch p := resp.GetPayload().(type) {
		case *pb.StreamResponse_StatusUpdate:
			evs = append(evs, ev{kind: "status", state: p.StatusUpdate.GetStatus().GetState()})
		case *pb.StreamResponse_ArtifactUpdate:
			au := p.ArtifactUpdate
			evs = append(evs, ev{kind: "artifact", text: artifactText(au.GetArtifact()), app: au.GetAppend(), last: au.GetLastChunk()})
		}
	}
	if len(evs) != 5 {
		t.Fatalf("got %d events: %+v", len(evs), evs)
	}
	if evs[0].kind != "status" || evs[0].state != pb.TaskState_TASK_STATE_WORKING {
		t.Errorf("evs[0] = %+v, want status WORKING", evs[0])
	}
	if evs[1].kind != "artifact" || evs[1].text != "a" || evs[1].app {
		t.Errorf("evs[1] = %+v, want artifact a append=false", evs[1])
	}
	if evs[2].kind != "artifact" || evs[2].text != "b" || !evs[2].app {
		t.Errorf("evs[2] = %+v, want artifact b append=true", evs[2])
	}
	if evs[3].kind != "artifact" || !evs[3].last || evs[3].text != "ab" {
		t.Errorf("evs[3] = %+v, want artifact ab lastChunk", evs[3])
	}
	if evs[4].kind != "status" || evs[4].state != pb.TaskState_TASK_STATE_COMPLETED {
		t.Errorf("evs[4] = %+v, want status COMPLETED", evs[4])
	}
}

type grpcFailHandler struct{}

func (grpcFailHandler) HandleMessage(_ context.Context, _ a2a.Message) ([]a2a.Part, error) {
	return nil, errors.New("boom")
}

func TestGRPCStreamError(t *testing.T) {
	cli, cleanup := dialBuf(t, grpcFailHandler{})
	defer cleanup()
	if _, err := cli.SendMessageStream(context.Background(), "hi", nil); err == nil {
		t.Error("expected an error from a failing handler over the gRPC stream")
	}
}
