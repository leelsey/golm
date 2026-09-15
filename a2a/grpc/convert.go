// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package grpc

import (
	"encoding/base64"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/leelsey/golm/a2a"
	pb "github.com/leelsey/golm/a2a/grpc/internal/a2apb"
)

func stateToPB(s a2a.TaskState) pb.TaskState {
	switch s {
	case a2a.StateSubmitted:
		return pb.TaskState_TASK_STATE_SUBMITTED
	case a2a.StateWorking:
		return pb.TaskState_TASK_STATE_WORKING
	case a2a.StateInputRequired:
		return pb.TaskState_TASK_STATE_INPUT_REQUIRED
	case a2a.StateCompleted:
		return pb.TaskState_TASK_STATE_COMPLETED
	case a2a.StateFailed:
		return pb.TaskState_TASK_STATE_FAILED
	case a2a.StateCanceled:
		return pb.TaskState_TASK_STATE_CANCELED
	case a2a.StateRejected:
		return pb.TaskState_TASK_STATE_REJECTED
	default:
		return pb.TaskState_TASK_STATE_UNSPECIFIED
	}
}

func tsToPB(rfc3339 string) *timestamppb.Timestamp {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return nil
	}
	return timestamppb.New(t)
}

func roleToPB(role string) pb.Role {
	switch role {
	case "agent":
		return pb.Role_ROLE_AGENT
	case "user":
		return pb.Role_ROLE_USER
	default:
		return pb.Role_ROLE_UNSPECIFIED
	}
}

func msgToPB(m *a2a.Message) *pb.Message {
	if m == nil {
		return nil
	}
	return &pb.Message{
		Role: roleToPB(m.Role), Parts: toParts(m.Parts),
		MessageId: m.MessageID, ContextId: m.ContextID, TaskId: m.TaskID,
	}
}

func statusToPB(st a2a.TaskStatus) *pb.TaskStatus {
	return &pb.TaskStatus{State: stateToPB(st.State), Timestamp: tsToPB(st.Timestamp), Message: msgToPB(st.Message)}
}

func artifactToPB(a a2a.Artifact) *pb.Artifact {
	return &pb.Artifact{ArtifactId: a.ArtifactID, Name: a.Name, Description: a.Description, Parts: toParts(a.Parts)}
}

func taskToPB(t a2a.Task) *pb.Task {
	arts := make([]*pb.Artifact, 0, len(t.Artifacts))
	for _, a := range t.Artifacts {
		arts = append(arts, artifactToPB(a))
	}
	hist := make([]*pb.Message, 0, len(t.History))
	for i := range t.History {
		hist = append(hist, msgToPB(&t.History[i]))
	}
	return &pb.Task{Id: t.ID, ContextId: t.ContextID, Status: statusToPB(t.Status), Artifacts: arts, History: hist}
}

func stateFromPB(s pb.TaskState) a2a.TaskState {
	switch s {
	case pb.TaskState_TASK_STATE_SUBMITTED:
		return a2a.StateSubmitted
	case pb.TaskState_TASK_STATE_WORKING:
		return a2a.StateWorking
	case pb.TaskState_TASK_STATE_INPUT_REQUIRED:
		return a2a.StateInputRequired
	case pb.TaskState_TASK_STATE_COMPLETED:
		return a2a.StateCompleted
	case pb.TaskState_TASK_STATE_FAILED:
		return a2a.StateFailed
	case pb.TaskState_TASK_STATE_CANCELED:
		return a2a.StateCanceled
	case pb.TaskState_TASK_STATE_REJECTED:
		return a2a.StateRejected
	default:
		return ""
	}
}

func roleFromPB(r pb.Role) string {
	switch r {
	case pb.Role_ROLE_AGENT:
		return "agent"
	case pb.Role_ROLE_USER:
		return "user"
	default:
		return ""
	}
}

func partsFromPB(parts []*pb.Part) []a2a.Part {
	out := make([]a2a.Part, 0, len(parts))
	for _, p := range parts {
		switch c := p.GetContent().(type) {
		case *pb.Part_Text:
			out = append(out, a2a.TextPart(c.Text))
		case *pb.Part_Raw:
			out = append(out, a2a.Part{File: &a2a.FilePart{
				Name: p.GetFilename(), MimeType: p.GetMediaType(),
				Bytes: base64.StdEncoding.EncodeToString(c.Raw)}})
		case *pb.Part_Url:
			out = append(out, a2a.Part{File: &a2a.FilePart{
				Name: p.GetFilename(), MimeType: p.GetMediaType(), URI: c.Url}})
		case *pb.Part_Data:
			if c.Data != nil {
				if b, err := c.Data.MarshalJSON(); err == nil {
					out = append(out, a2a.Part{Data: b})
				}
			}
		}
	}
	return out
}

func artifactText(a *pb.Artifact) string {
	var b strings.Builder
	for _, p := range a.GetParts() {
		if t, ok := p.GetContent().(*pb.Part_Text); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

func msgFromPB(m *pb.Message) *a2a.Message {
	if m == nil {
		return nil
	}
	return &a2a.Message{
		Role: roleFromPB(m.GetRole()), Parts: partsFromPB(m.GetParts()),
		MessageID: m.GetMessageId(), ContextID: m.GetContextId(), TaskID: m.GetTaskId(),
	}
}

func statusFromPB(st *pb.TaskStatus) a2a.TaskStatus {
	if st == nil {
		return a2a.TaskStatus{}
	}
	out := a2a.TaskStatus{State: stateFromPB(st.GetState()), Message: msgFromPB(st.GetMessage())}
	if ts := st.GetTimestamp(); ts != nil {
		out.Timestamp = ts.AsTime().UTC().Format(time.RFC3339)
	}
	return out
}

func artifactFromPB(a *pb.Artifact) a2a.Artifact {
	if a == nil {
		return a2a.Artifact{}
	}
	return a2a.Artifact{ArtifactID: a.GetArtifactId(), Name: a.GetName(), Description: a.GetDescription(), Parts: partsFromPB(a.GetParts())}
}

func taskFromPB(t *pb.Task) a2a.Task {
	if t == nil {
		return a2a.Task{}
	}
	arts := make([]a2a.Artifact, 0, len(t.GetArtifacts()))
	for _, a := range t.GetArtifacts() {
		arts = append(arts, artifactFromPB(a))
	}
	hist := make([]a2a.Message, 0, len(t.GetHistory()))
	for _, m := range t.GetHistory() {
		if mm := msgFromPB(m); mm != nil {
			hist = append(hist, *mm)
		}
	}
	return a2a.Task{ID: t.GetId(), ContextID: t.GetContextId(), Status: statusFromPB(t.GetStatus()), Artifacts: arts, History: hist, Kind: "task"}
}
