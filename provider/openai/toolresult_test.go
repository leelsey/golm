// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

func imageResultRequest() golm.Request {
	return golm.Request{
		Model: "gpt-4o",
		Messages: []golm.Message{{Role: golm.RoleTool, Content: []golm.Content{golm.ToolResult{
			ToolUseID: "t1",
			Name:      "screenshot",
			Content:   []golm.ToolContent{golm.Text{Text: "captured"}, golm.Image{MediaType: "image/png", Data: []byte("png")}},
		}}}},
	}
}

// The refusal is under the shared guard contract; the claim is this wire format's own.
func TestToolResultImagesNotClaimed(t *testing.T) {
	if New("key").Capabilities().ToolResultImages {
		t.Error("ToolResultImages should be false while tool messages are a plain string")
	}
}

func TestToolResultTextUnchanged(t *testing.T) {
	body := captureBody(t, golm.Request{
		Model: "gpt-4o",
		Messages: []golm.Message{{Role: golm.RoleTool, Content: []golm.Content{
			golm.ToolResult{ToolUseID: "t1", Name: "echo", Content: golm.ToolText("echoed")},
		}}},
	})
	if !strings.Contains(body, `"content":"echoed"`) || !strings.Contains(body, `"tool_call_id":"t1"`) {
		t.Errorf("text tool result not sent as a plain string: %s", body)
	}
}

// The guard has to sit where every message passes, not only the tool-role arm.
func TestToolResultImageRefusedOutsideAToolTurn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("request sent despite a tool result this API cannot carry")
	}))
	defer srv.Close()

	req := imageResultRequest()
	req.Messages[0].Role = golm.RoleUser

	_, err := New("key").WithBaseURL(srv.URL).Complete(context.Background(), req)
	if err == nil {
		t.Fatal("Complete accepted an image tool result in a user turn")
	}
	if !strings.Contains(err.Error(), "screenshot") {
		t.Errorf("err = %v, want one naming the tool", err)
	}
}
