// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package grpc

import (
	"encoding/base64"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/leelsey/golm/a2a"
	pb "github.com/leelsey/golm/a2a/grpc/internal/a2apb"
)

// These decode what a REMOTE agent sent over gRPC.
func TestRoleAndStateFromPBRefuseToGuess(t *testing.T) {
	if got := roleFromPB(pb.Role_ROLE_USER); got != "user" {
		t.Errorf("user role = %q", got)
	}
	if got := roleFromPB(pb.Role_ROLE_AGENT); got != "agent" {
		t.Errorf("agent role = %q", got)
	}
	for _, r := range []pb.Role{pb.Role_ROLE_UNSPECIFIED, pb.Role(9999), pb.Role(-1)} {
		if got := roleFromPB(r); got != "" {
			t.Errorf("unknown role %v became %q; it must not be guessed", r, got)
		}
	}
	for _, s := range []pb.TaskState{pb.TaskState_TASK_STATE_UNSPECIFIED, pb.TaskState(4242)} {
		if got := stateFromPB(s); got != "" {
			t.Errorf("unknown state %v became %q", s, got)
		}
	}
}

// Every part variant the wire can carry must survive.
func TestPartsFromPBCoversEveryVariant(t *testing.T) {
	data, err := structpb.NewValue(map[string]any{"k": "v"})
	if err != nil {
		t.Fatal(err)
	}
	in := []*pb.Part{
		{Content: &pb.Part_Text{Text: "hello"}},
		{Content: &pb.Part_Raw{Raw: []byte{1, 2, 3}}, Filename: "f.bin", MediaType: "application/octet-stream"},
		{Content: &pb.Part_Url{Url: "https://example.invalid/a"}, Filename: "a", MediaType: "text/plain"},
		{Content: &pb.Part_Data{Data: data}},
		{Content: &pb.Part_Data{Data: nil}},
		{},
	}
	out := partsFromPB(in)
	if len(out) != 4 {
		t.Fatalf("got %d parts, want 4 (the two empty ones dropped): %+v", len(out), out)
	}
	if out[0].Text != "hello" {
		t.Errorf("text part = %+v", out[0])
	}
	if out[1].File == nil || out[1].File.Bytes != base64.StdEncoding.EncodeToString([]byte{1, 2, 3}) {
		t.Errorf("raw part = %+v", out[1].File)
	}
	if out[1].File.Name != "f.bin" || out[1].File.MimeType != "application/octet-stream" {
		t.Errorf("raw part lost its filename or media type: %+v", out[1].File)
	}
	if out[2].File == nil || out[2].File.URI != "https://example.invalid/a" {
		t.Errorf("url part = %+v", out[2].File)
	}
	if len(out[3].Data) == 0 {
		t.Error("data part lost its payload")
	}
	if got := partsFromPB(nil); len(got) != 0 {
		t.Errorf("nil parts became %d", len(got))
	}
}

// Nil is what a peer sends when it sends nothing.
func TestConvertersAreNilSafe(t *testing.T) {
	if msgFromPB(nil) != nil {
		t.Error("msgFromPB(nil) invented a message")
	}
	if got := statusFromPB(nil); got != (a2a.TaskStatus{}) {
		t.Errorf("statusFromPB(nil) = %+v", got)
	}
	if got := artifactFromPB(nil); got.ArtifactID != "" || len(got.Parts) != 0 {
		t.Errorf("artifactFromPB(nil) = %+v", got)
	}
	if got := taskFromPB(nil); got.ID != "" {
		t.Errorf("taskFromPB(nil) = %+v", got)
	}
	if got := artifactText(nil); got != "" {
		t.Errorf("artifactText(nil) = %q", got)
	}
}

// An artifact carries its identity and its parts.
func TestArtifactFromPBKeepsIdentityAndParts(t *testing.T) {
	got := artifactFromPB(&pb.Artifact{
		ArtifactId: "art-1", Name: "answer", Description: "the answer",
		Parts: []*pb.Part{{Content: &pb.Part_Text{Text: "forty-two"}}},
	})
	if got.ArtifactID != "art-1" || got.Name != "answer" || got.Description != "the answer" {
		t.Errorf("identity lost: %+v", got)
	}
	if len(got.Parts) != 1 || got.Parts[0].Text != "forty-two" {
		t.Errorf("parts lost: %+v", got.Parts)
	}
	if got := artifactText(&pb.Artifact{Parts: []*pb.Part{
		{Content: &pb.Part_Text{Text: "a"}},
		{Content: &pb.Part_Raw{Raw: []byte{1}}},
		{Content: &pb.Part_Text{Text: "b"}},
	}}); got != "ab" {
		t.Errorf("artifactText = %q, want the text parts joined", got)
	}
}

// A status timestamp arrives as a protobuf Timestamp and leaves as RFC3339 in UTC.
func TestStatusFromPBNormalisesTheTimestamp(t *testing.T) {
	got := statusFromPB(&pb.TaskStatus{
		State:     pb.TaskState_TASK_STATE_WORKING,
		Timestamp: timestamppb.New(mustTime(t, "2026-09-15T10:30:00Z")),
	})
	if got.State != a2a.StateWorking {
		t.Errorf("state = %q", got.State)
	}
	if got.Timestamp != "2026-09-15T10:30:00Z" {
		t.Errorf("timestamp = %q, want RFC3339 UTC", got.Timestamp)
	}

	if got := statusFromPB(&pb.TaskStatus{State: pb.TaskState_TASK_STATE_WORKING}); got.Timestamp != "" {
		t.Errorf("a missing timestamp became %q", got.Timestamp)
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}
