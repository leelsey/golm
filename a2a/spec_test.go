// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package a2a

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/internal/jsonrpc"
)

func TestToGolmMessageRejectsUnsupportedFile(t *testing.T) {
	_, err := toGolmMessage(Message{Parts: []Part{
		{File: &FilePart{Name: "doc.pdf", MimeType: "application/pdf", Bytes: base64.StdEncoding.EncodeToString([]byte("PDF"))}},
		TextPart("hi"),
	}})
	if err == nil || !strings.Contains(err.Error(), "application/pdf") {
		t.Fatalf("err = %v, want an unsupported-media-type error naming the MIME type", err)
	}
	if _, err := toGolmMessage(Message{Parts: []Part{
		{File: &FilePart{Name: "scan.png", MimeType: "image/png", URI: "https://example.com/scan.png"}},
	}}); err == nil {
		t.Fatal("URI-only file part accepted; it is invisible to the agent and must error")
	}
	m, err := toGolmMessage(Message{Parts: []Part{
		{File: &FilePart{MimeType: "image/png", Bytes: base64.StdEncoding.EncodeToString([]byte{1})}},
		TextPart("hi"),
	}})
	if err != nil {
		t.Fatalf("image part: %v", err)
	}
	if _, ok := m.Content[0].(golm.Image); !ok {
		t.Error("image file part not mapped to golm.Image")
	}
}

func TestRPCRejectsBadVersion(t *testing.T) {
	hs := httptest.NewServer(NewServer(CardForAgent("e", "e", "http://x/"), echoHandler{}).HTTPHandler())
	defer hs.Close()

	resp, err := http.Post(hs.URL+"/", "application/json",
		strings.NewReader(`{"jsonrpc":"1.0","id":1,"method":"message/send","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var rpcResp jsonrpc.Response
	_ = json.NewDecoder(resp.Body).Decode(&rpcResp)
	if rpcResp.Error == nil || rpcResp.Error.Code != jsonrpc.CodeInvalidRequest {
		t.Errorf("bad jsonrpc version not rejected: %+v", rpcResp.Error)
	}
}

type recSink struct {
	finals []bool
	states []TaskState
}

func (r *recSink) OnTask(string, string) {}
func (r *recSink) Status(s TaskStatus, final bool) error {
	r.finals = append(r.finals, final)
	r.states = append(r.states, s.State)
	return nil
}
func (r *recSink) Artifact(Artifact, bool, bool) error { return nil }

func TestEmitFinalForcesFinalWhenNoTerminal(t *testing.T) {
	b := newTaskBroker(0)
	b.publish(taskEvent{kind: "status", status: TaskStatus{State: StateWorking}, final: false})
	rs := &recSink{}
	b.emitFinal(rs)

	if len(rs.finals) != 1 || !rs.finals[0] || rs.states[0] != StateWorking {
		t.Errorf("emit = finals:%v states:%v, want a single forced-final working status", rs.finals, rs.states)
	}
}

func TestEmitFinalTerminal(t *testing.T) {
	b := newTaskBroker(0)
	b.publish(taskEvent{kind: "status", status: TaskStatus{State: StateWorking}, final: false})
	b.publish(taskEvent{kind: "status", status: TaskStatus{State: StateCompleted}, final: true})
	rs := &recSink{}
	b.emitFinal(rs)
	if len(rs.finals) != 1 || !rs.finals[0] || rs.states[0] != StateCompleted {
		t.Errorf("terminal emit = finals:%v states:%v", rs.finals, rs.states)
	}
}
