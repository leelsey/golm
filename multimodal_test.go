// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestMultimodalJSONRoundTrip(t *testing.T) {
	in := Message{Role: RoleUser, Content: []Content{
		Text{Text: "look"},
		Image{MediaType: "image/png", Data: []byte{1, 2, 3}},
		Audio{MediaType: "audio/wav", Data: []byte{4, 5, 6}},
	}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Message
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	img, ok := out.Content[1].(Image)
	if !ok || img.MediaType != "image/png" || !bytes.Equal(img.Data, []byte{1, 2, 3}) {
		t.Errorf("image lost: %+v", out.Content[1])
	}
	aud, ok := out.Content[2].(Audio)
	if !ok || aud.MediaType != "audio/wav" || !bytes.Equal(aud.Data, []byte{4, 5, 6}) {
		t.Errorf("audio lost: %+v", out.Content[2])
	}
}

func TestRunMessageDeliversMultimodal(t *testing.T) {
	fp := &fakeProvider{responses: []Response{{Message: AssistantText("seen"), StopReason: StopEndTurn}}}
	a := &Agent{Provider: fp, Model: "x"}
	msg := Message{Role: RoleUser, Content: []Content{
		Text{Text: "describe"},
		Image{MediaType: "image/png", Data: []byte{9}},
	}}
	res, err := a.RunMessage(context.Background(), NewSession(), msg)
	if err != nil {
		t.Fatalf("RunMessage: %v", err)
	}
	if res.Text() != "seen" {
		t.Errorf("res = %q", res.Text())
	}
	last := fp.lastReq.Messages[len(fp.lastReq.Messages)-1]
	hasImage := false
	for _, c := range last.Content {
		if _, ok := c.(Image); ok {
			hasImage = true
		}
	}
	if !hasImage {
		t.Error("image content was not delivered to the provider request")
	}
}
