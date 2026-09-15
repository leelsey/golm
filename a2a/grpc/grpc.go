// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

// Package grpc is the A2A gRPC transport binding for golm, built on the official a2a.proto.
package grpc

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/a2a"
	pb "github.com/leelsey/golm/a2a/grpc/internal/a2apb"
)

func textOf(m *pb.Message) string {
	if m == nil {
		return ""
	}
	var b strings.Builder
	for _, p := range m.GetParts() {
		if t, ok := p.GetContent().(*pb.Part_Text); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

func textPart(s string) *pb.Part { return &pb.Part{Content: &pb.Part_Text{Text: s}} }

func taskText(t *pb.Task) string {
	if t == nil {
		return ""
	}
	var b strings.Builder
	for _, a := range t.GetArtifacts() {
		b.WriteString(artifactText(a))
	}
	return b.String()
}

func toParts(parts []a2a.Part) []*pb.Part {
	out := make([]*pb.Part, 0, len(parts))
	for _, p := range parts {
		switch {
		case p.File != nil:
			pbp := &pb.Part{Filename: p.File.Name, MediaType: p.File.MimeType}
			if p.File.URI != "" {
				pbp.Content = &pb.Part_Url{Url: p.File.URI}
			} else if raw, err := base64.StdEncoding.DecodeString(p.File.Bytes); err == nil && len(raw) > 0 {
				pbp.Content = &pb.Part_Raw{Raw: raw}
			} else {
				out = append(out, textPart(p.Text))
				continue
			}
			out = append(out, pbp)
		case len(p.Data) > 0:
			v := &structpb.Value{}
			if err := v.UnmarshalJSON(p.Data); err == nil {
				out = append(out, &pb.Part{Content: &pb.Part_Data{Data: v}})
			} else {
				out = append(out, textPart(p.Text))
			}
		default:
			out = append(out, textPart(p.Text))
		}
	}
	return out
}

// Server implements the A2A gRPC service backed by an a2a.Handler and a shared a2a.TaskStore.
type Server struct {
	pb.UnimplementedA2AServiceServer
	handler a2a.Handler
	store   *a2a.TaskStore
}

func toStatus(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

func (s *Server) input(req *pb.SendMessageRequest) a2a.Message {
	parts := partsFromPB(req.GetMessage().GetParts())
	if len(parts) == 0 {
		parts = []a2a.Part{a2a.TextPart("")}
	}
	return a2a.Message{Role: "user", Parts: parts}
}

// SendMessage runs the handler and returns the resulting task.
func (s *Server) SendMessage(ctx context.Context, req *pb.SendMessageRequest) (*pb.SendMessageResponse, error) {
	if req.GetConfiguration().GetReturnImmediately() {
		task := s.store.SendAsync(s.handler, s.input(req))
		return &pb.SendMessageResponse{Payload: &pb.SendMessageResponse_Task{Task: taskToPB(task)}}, nil
	}
	task, err := s.store.Send(ctx, s.handler, s.input(req))
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.SendMessageResponse{Payload: &pb.SendMessageResponse_Task{Task: taskToPB(task)}}, nil
}

type pbSink struct {
	stream grpc.ServerStreamingServer[pb.StreamResponse]
	taskID string
	ctxID  string
	err    error
}

func (k *pbSink) OnTask(taskID, contextID string) { k.taskID, k.ctxID = taskID, contextID }

func (k *pbSink) send(r *pb.StreamResponse) error {
	if k.err != nil {
		return k.err
	}
	k.err = k.stream.Send(r)
	return k.err
}

func (k *pbSink) Status(st a2a.TaskStatus, final bool) error {
	return k.send(&pb.StreamResponse{Payload: &pb.StreamResponse_StatusUpdate{
		StatusUpdate: &pb.TaskStatusUpdateEvent{TaskId: k.taskID, ContextId: k.ctxID, Status: statusToPB(st)}}})
}

func (k *pbSink) Artifact(a a2a.Artifact, appendChunk, lastChunk bool) error {
	return k.send(&pb.StreamResponse{Payload: &pb.StreamResponse_ArtifactUpdate{
		ArtifactUpdate: &pb.TaskArtifactUpdateEvent{
			TaskId: k.taskID, ContextId: k.ctxID, Artifact: artifactToPB(a), Append: appendChunk, LastChunk: lastChunk}}})
}

// SendStreamingMessage streams status/artifact update events as the task runs.
func (s *Server) SendStreamingMessage(req *pb.SendMessageRequest, stream grpc.ServerStreamingServer[pb.StreamResponse]) error {
	sink := &pbSink{stream: stream}
	s.store.SendStream(stream.Context(), s.handler, s.input(req), sink)
	return sink.err
}

// GetTask returns a stored task by id.
func (s *Server) GetTask(_ context.Context, req *pb.GetTaskRequest) (*pb.Task, error) {
	task, ok := s.store.Get(req.GetId())
	if !ok {
		return nil, status.Error(codes.NotFound, "task not found")
	}
	return taskToPB(task), nil
}

// CancelTask cancels an in-flight task.
func (s *Server) CancelTask(_ context.Context, req *pb.CancelTaskRequest) (*pb.Task, error) {
	task, err := s.store.Cancel(req.GetId())
	switch {
	case errors.Is(err, a2a.ErrTaskNotFound):
		return nil, status.Error(codes.NotFound, "task not found")
	case errors.Is(err, a2a.ErrTaskNotCancelable):
		return nil, status.Error(codes.FailedPrecondition, "task cannot be canceled")
	default:
		return taskToPB(task), nil
	}
}

// ListTasks returns stored tasks, optionally filtered by context id and state, honouring page_size.
func (s *Server) ListTasks(_ context.Context, req *pb.ListTasksRequest) (*pb.ListTasksResponse, error) {
	ctxFilter := req.GetContextId()
	stateFilter := req.GetStatus()
	var all []*pb.Task
	for _, t := range s.store.List() {
		if ctxFilter != "" && t.ContextID != ctxFilter {
			continue
		}
		if stateFilter != pb.TaskState_TASK_STATE_UNSPECIFIED && stateToPB(t.Status.State) != stateFilter {
			continue
		}
		all = append(all, taskToPB(t))
	}

	sort.Slice(all, func(i, j int) bool { return all[i].GetId() < all[j].GetId() })
	pageSize := req.GetPageSize()
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 50
	}
	start := 0
	if tok := req.GetPageToken(); tok != "" {
		if n, err := strconv.Atoi(tok); err == nil && n > 0 {
			start = n
		}
	}
	if start > len(all) {
		start = len(all)
	}
	end := start + int(pageSize)
	if end > len(all) {
		end = len(all)
	}
	var next string
	if end < len(all) {
		next = strconv.Itoa(end)
	}
	return &pb.ListTasksResponse{
		Tasks: all[start:end], TotalSize: int32(len(all)), PageSize: pageSize, NextPageToken: next,
	}, nil
}

// SubscribeToTask replays then live-streams a task's lifecycle events.
func (s *Server) SubscribeToTask(req *pb.SubscribeToTaskRequest, stream grpc.ServerStreamingServer[pb.StreamResponse]) error {
	task, ok := s.store.Get(req.GetId())
	if !ok {
		return status.Error(codes.NotFound, "task not found")
	}
	sink := &pbSink{stream: stream, taskID: task.ID, ctxID: task.ContextID}
	if s.store.Subscribe(stream.Context(), req.GetId(), sink) {
		return sink.err
	}

	if fresh, ok := s.store.Get(req.GetId()); ok {
		task = fresh
	}
	if err := sink.Status(task.Status, true); err != nil {
		return err
	}
	if len(task.Artifacts) > 0 {
		return sink.Artifact(task.Artifacts[0], false, true)
	}
	return nil
}

// TaskStore returns the server's task store.
func (s *Server) TaskStore() *a2a.TaskStore { return s.store }

// RegisterServer registers an A2A gRPC service backed by h onto g, with its own task store.
func RegisterServer(g *grpc.Server, h a2a.Handler) *Server {
	srv := &Server{handler: h, store: a2a.NewTaskStore()}
	pb.RegisterA2AServiceServer(g, srv)
	return srv
}

// Serve starts a gRPC A2A server on addr backed by h, stopping when ctx is done.
func Serve(ctx context.Context, addr string, h a2a.Handler) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	g := grpc.NewServer()
	srv := RegisterServer(g, h)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			srv.store.Shutdown(sctx)
			cancel()
			g.GracefulStop()
		case <-done:
		}
	}()
	return g.Serve(lis)
}

// Client calls a remote A2A agent over gRPC.
type Client struct {
	conn *grpc.ClientConn
	cli  pb.A2AServiceClient
}

// Dial connects to a remote A2A gRPC server at addr.
func Dial(addr string, opts ...grpc.DialOption) (*Client, error) {
	if len(opts) == 0 {
		opts = []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	}
	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, cli: pb.NewA2AServiceClient(conn)}, nil
}

// NewClient wraps an existing gRPC connection.
func NewClient(conn *grpc.ClientConn) *Client {
	return &Client{conn: conn, cli: pb.NewA2AServiceClient(conn)}
}

func userRequest(text string) *pb.SendMessageRequest {
	return &pb.SendMessageRequest{Message: &pb.Message{Role: pb.Role_ROLE_USER, Parts: []*pb.Part{textPart(text)}}}
}

func pbTaskFailure(t *pb.Task) error {
	switch st := t.GetStatus().GetState(); st {
	case pb.TaskState_TASK_STATE_FAILED, pb.TaskState_TASK_STATE_CANCELED, pb.TaskState_TASK_STATE_REJECTED:
		return fmt.Errorf("a2a: task %s: %s", stateFromPB(st), textOf(t.GetStatus().GetMessage()))
	}
	return nil
}

// SendMessage sends text and returns the remote agent's response text.
func (c *Client) SendMessage(ctx context.Context, text string) (string, error) {
	resp, err := c.cli.SendMessage(ctx, userRequest(text))
	if err != nil {
		return "", err
	}
	switch p := resp.GetPayload().(type) {
	case *pb.SendMessageResponse_Task:
		if err := pbTaskFailure(p.Task); err != nil {
			return "", err
		}
		return taskText(p.Task), nil
	case *pb.SendMessageResponse_Message:
		return textOf(p.Message), nil
	default:
		return "", nil
	}
}

// SendMessageStream sends text and invokes onDelta for each streamed chunk, returning the assembled text.
func (c *Client) SendMessageStream(ctx context.Context, text string, onDelta func(string)) (string, error) {
	stream, err := c.cli.SendStreamingMessage(ctx, userRequest(text))
	if err != nil {
		return "", err
	}
	var asm a2a.StreamAssembler
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch p := resp.GetPayload().(type) {
		case *pb.StreamResponse_StatusUpdate:
			switch st := p.StatusUpdate.GetStatus().GetState(); st {
			case pb.TaskState_TASK_STATE_FAILED, pb.TaskState_TASK_STATE_CANCELED, pb.TaskState_TASK_STATE_REJECTED:
				return "", fmt.Errorf("a2a: task %s: %s", stateFromPB(st), textOf(p.StatusUpdate.GetStatus().GetMessage()))
			}
		case *pb.StreamResponse_ArtifactUpdate:
			au := p.ArtifactUpdate
			t := artifactText(au.GetArtifact())
			if au.GetLastChunk() {
				asm.Final(t)
				continue
			}
			asm.Delta(t, au.GetAppend())
			if onDelta != nil {
				onDelta(t)
			}
		case *pb.StreamResponse_Message:
			t := textOf(p.Message)
			asm.Delta(t, true)
			if onDelta != nil {
				onDelta(t)
			}
		case *pb.StreamResponse_Task:
			if err := pbTaskFailure(p.Task); err != nil {
				return "", err
			}

			switch p.Task.GetStatus().GetState() {
			case pb.TaskState_TASK_STATE_COMPLETED, pb.TaskState_TASK_STATE_INPUT_REQUIRED, pb.TaskState_TASK_STATE_AUTH_REQUIRED:
				asm.Final(taskText(p.Task))
			}
		}
	}
	return asm.Result(), nil
}

// GetTask fetches a task by id.
func (c *Client) GetTask(ctx context.Context, id string) (a2a.Task, error) {
	t, err := c.cli.GetTask(ctx, &pb.GetTaskRequest{Id: id})
	if err != nil {
		return a2a.Task{}, err
	}
	return taskFromPB(t), nil
}

// CancelTask requests cancellation of a task by id.
func (c *Client) CancelTask(ctx context.Context, id string) (a2a.Task, error) {
	t, err := c.cli.CancelTask(ctx, &pb.CancelTaskRequest{Id: id})
	if err != nil {
		return a2a.Task{}, err
	}
	return taskFromPB(t), nil
}

// ListTasks lists tasks, optionally filtered by context id and state.
func (c *Client) ListTasks(ctx context.Context, contextID string, state a2a.TaskState) ([]a2a.Task, error) {
	resp, err := c.cli.ListTasks(ctx, &pb.ListTasksRequest{ContextId: contextID, Status: stateToPB(state)})
	if err != nil {
		return nil, err
	}
	out := make([]a2a.Task, 0, len(resp.GetTasks()))
	for _, t := range resp.GetTasks() {
		out = append(out, taskFromPB(t))
	}
	return out, nil
}

// SubscribeToTask subscribes to a task and returns its assembled state after the stream ends.
func (c *Client) SubscribeToTask(ctx context.Context, id string) (a2a.Task, error) {
	stream, err := c.cli.SubscribeToTask(ctx, &pb.SubscribeToTaskRequest{Id: id})
	if err != nil {
		return a2a.Task{}, err
	}
	var task a2a.Task
	task.ID = id
	var final a2a.Artifact
	var deltas strings.Builder
	var deltaMeta a2a.Artifact
	var fullTask *a2a.Task
	sawDelta, lastSeen := false, false
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return a2a.Task{}, err
		}
		switch p := resp.GetPayload().(type) {
		case *pb.StreamResponse_StatusUpdate:
			task.Status = statusFromPB(p.StatusUpdate.GetStatus())
			if tid := p.StatusUpdate.GetTaskId(); tid != "" {
				task.ID = tid
			}
		case *pb.StreamResponse_ArtifactUpdate:
			au := p.ArtifactUpdate
			if au.GetLastChunk() {
				final = artifactFromPB(au.GetArtifact())
				lastSeen = true
				continue
			}
			if !sawDelta {
				deltaMeta = artifactFromPB(au.GetArtifact())
			}
			sawDelta = true
			if !au.GetAppend() {
				deltas.Reset()
			}
			deltas.WriteString(artifactText(au.GetArtifact()))
		case *pb.StreamResponse_Task:
			ft := taskFromPB(p.Task)
			fullTask = &ft
		case *pb.StreamResponse_Message:
			deltas.WriteString(textOf(p.Message))
			sawDelta = true
		}
	}
	if fullTask != nil {
		if fullTask.ID == "" {
			fullTask.ID = task.ID
		}
		return *fullTask, nil
	}

	switch {
	case lastSeen:
		task.Artifacts = []a2a.Artifact{final}
	case sawDelta:
		deltaMeta.Parts = []a2a.Part{a2a.TextPart(deltas.String())}
		task.Artifacts = []a2a.Artifact{deltaMeta}
	}
	return task, nil
}

// AsTool wraps the remote gRPC agent as a golm.Tool delegating a "task" string.
func (c *Client) AsTool(name, description string) golm.Tool {
	return golm.TextTool(name, description, "task", "the task to delegate to the remote agent",
		func(ctx context.Context, task string) (string, error) {
			return c.SendMessage(ctx, task)
		})
}

// Close closes the underlying connection.
func (c *Client) Close() error { return c.conn.Close() }
