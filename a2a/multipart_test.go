// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package a2a

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/leelsey/golm"
)

type captureProvider struct{ got golm.Message }

func (c *captureProvider) Name() string                    { return "capture" }
func (c *captureProvider) Capabilities() golm.Capabilities { return golm.Capabilities{Images: true} }

func (c *captureProvider) Complete(_ context.Context, req golm.Request) (golm.Response, error) {
	if len(req.Messages) > 0 {
		c.got = req.Messages[len(req.Messages)-1]
	}
	return golm.Response{Message: golm.AssistantText("ok"), StopReason: golm.StopEndTurn}, nil
}

func (c *captureProvider) Stream(ctx context.Context, req golm.Request, _ func(golm.StreamEvent) error) (golm.Response, error) {
	return c.Complete(ctx, req)
}

func TestHandlerMapsFilePartToImage(t *testing.T) {
	cp := &captureProvider{}
	ag := &golm.Agent{Provider: cp, Model: "x"}
	h := AgentHandler(ag)
	img := base64.StdEncoding.EncodeToString([]byte{0x89, 0x50, 0x4e, 0x47})
	msg := Message{Role: "user", Parts: []Part{
		TextPart("describe"),
		{File: &FilePart{MimeType: "image/png", Bytes: img}},
	}}
	if _, err := h.HandleMessage(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	hasImage := false
	for _, c := range cp.got.Content {
		if _, ok := c.(golm.Image); ok {
			hasImage = true
		}
	}
	if !hasImage {
		t.Errorf("expected an Image to reach the agent, got %#v", cp.got.Content)
	}
}
