// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package grpc

import (
	"strings"
	"testing"

	pb "github.com/leelsey/golm/a2a/grpc/internal/a2apb"
)

func TestPBTaskFailure(t *testing.T) {
	failed := &pb.Task{Status: &pb.TaskStatus{
		State:   pb.TaskState_TASK_STATE_FAILED,
		Message: &pb.Message{Role: pb.Role_ROLE_AGENT, Parts: []*pb.Part{textPart("boom")}},
	}}
	if err := pbTaskFailure(failed); err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("failed task: err = %v, want a failure error", err)
	}
	ok := &pb.Task{Status: &pb.TaskStatus{State: pb.TaskState_TASK_STATE_COMPLETED}}
	if err := pbTaskFailure(ok); err != nil {
		t.Fatalf("completed task: err = %v, want nil", err)
	}
}
