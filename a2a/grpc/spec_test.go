// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package grpc

import (
	"context"
	"testing"

	pb "github.com/leelsey/golm/a2a/grpc/internal/a2apb"
)

func TestPartsFromPBMapsRawFile(t *testing.T) {
	parts := partsFromPB([]*pb.Part{{Content: &pb.Part_Raw{Raw: []byte{1, 2, 3}}, MediaType: "image/png"}})
	if len(parts) != 1 || parts[0].File == nil {
		t.Fatalf("raw part not mapped to a file part: %+v", parts)
	}
	if parts[0].File.MimeType != "image/png" || parts[0].File.Bytes == "" {
		t.Errorf("file part = %+v", parts[0].File)
	}
}

func TestListTasksPaging(t *testing.T) {
	cli, cleanup := dialBuf(t, quickAsync{})
	defer cleanup()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := cli.cli.SendMessage(ctx, &pb.SendMessageRequest{
			Message: &pb.Message{Role: pb.Role_ROLE_USER, Parts: []*pb.Part{textPart("go")}},
		}); err != nil {
			t.Fatal(err)
		}
	}

	pageSize := int32(2)
	resp, err := cli.cli.ListTasks(ctx, &pb.ListTasksRequest{PageSize: &pageSize})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(resp.GetTasks()); n != 2 {
		t.Errorf("page returned %d tasks, want 2", n)
	}
	if resp.GetTotalSize() != 3 {
		t.Errorf("total size = %d, want 3", resp.GetTotalSize())
	}
	if resp.GetNextPageToken() == "" {
		t.Error("expected a next page token when more tasks remain")
	}
}

func TestListTasksPagingCoversAllTasks(t *testing.T) {
	cli, cleanup := dialBuf(t, quickAsync{})
	defer cleanup()
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := cli.cli.SendMessage(ctx, &pb.SendMessageRequest{
			Message: &pb.Message{Role: pb.Role_ROLE_USER, Parts: []*pb.Part{textPart("go")}},
		}); err != nil {
			t.Fatal(err)
		}
	}

	seen := map[string]int{}
	pageSize := int32(2)
	token := ""
	for pages := 0; pages < 100; pages++ {
		resp, err := cli.cli.ListTasks(ctx, &pb.ListTasksRequest{PageSize: &pageSize, PageToken: token})
		if err != nil {
			t.Fatal(err)
		}
		for _, tk := range resp.GetTasks() {
			seen[tk.GetId()]++
		}
		token = resp.GetNextPageToken()
		if token == "" {
			break
		}
	}
	if len(seen) != 5 {
		t.Errorf("paged %d distinct tasks, want 5", len(seen))
	}
	for id, c := range seen {
		if c != 1 {
			t.Errorf("task %s seen %d times (skip/duplicate across pages)", id, c)
		}
	}
}
