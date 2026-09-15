// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package acp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/acp"
)

// The reason an ACP agent is worth more than the same agent with a workspace.
func TestReadFileComesFromTheEditor(t *testing.T) {
	a, _ := newAgent(t, say("read"))
	e := connect(t, a)
	e.initialize(t, fullCaps())
	e.mu.Lock()
	e.files["/tmp/x.go"] = "package main // UNSAVED EDIT"
	e.mu.Unlock()
	id := e.newSession(t)

	tools := a.FileTools()
	if len(tools) != 2 {
		t.Fatalf("%d file tools, want read and write", len(tools))
	}
	byName := map[string]golm.Tool{}
	for _, tool := range tools {
		byName[tool.Name()] = tool
	}
	ctx := acp.SessionContext(context.Background(), id)
	out, err := byName["read_file"].Execute(ctx, json.RawMessage(`{"path":"/tmp/x.go"}`))
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if got := toolText(out); !strings.Contains(got, "UNSAVED EDIT") {
		t.Errorf("read %q; the agent should see the editor's buffer", got)
	}

	if tr, ok := golm.TraitsOf(byName["read_file"]); !ok || !tr.Filesystem || !tr.ReadOnly {
		t.Errorf("read_file traits = %+v (declared %v)", tr, ok)
	}
}

func TestWriteFileGoesThroughTheEditor(t *testing.T) {
	a, _ := newAgent(t, say("wrote"))
	e := connect(t, a)
	e.initialize(t, fullCaps())
	id := e.newSession(t)

	var write golm.Tool
	for _, tool := range a.FileTools() {
		if tool.Name() == "write_file" {
			write = tool
		}
	}
	if write == nil {
		t.Fatal("no write_file tool")
	}
	ctx := acp.SessionContext(context.Background(), id)
	if _, err := write.Execute(ctx, json.RawMessage(`{"path":"/tmp/y.go","content":"new body"}`)); err != nil {
		t.Fatalf("write_file: %v", err)
	}
	e.mu.Lock()
	got := e.writes["/tmp/y.go"]
	e.mu.Unlock()
	if got != "new body" {
		t.Errorf("the editor received %q", got)
	}
	if tr, ok := golm.TraitsOf(write); !ok || tr.ReadOnly {
		t.Errorf("write_file must not claim to be read-only: %+v", tr)
	}
}

// Offering a tool that cannot work is worse than not offering it.
func TestFileToolsFollowTheClientCapabilities(t *testing.T) {
	cases := []struct {
		name string
		caps acp.ClientCapabilities
		want []string
	}{
		{"none", acp.ClientCapabilities{}, nil},
		{"read only", acp.ClientCapabilities{FS: acp.FileSystemCapabilities{ReadTextFile: true}}, []string{"read_file"}},
		{"write only", acp.ClientCapabilities{FS: acp.FileSystemCapabilities{WriteTextFile: true}}, []string{"write_file"}},
		{"both", fullCaps(), []string{"read_file", "write_file"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, _ := newAgent(t, say("x"))
			e := connect(t, a)
			e.initialize(t, c.caps)
			var names []string
			for _, tool := range a.FileTools() {
				names = append(names, tool.Name())
			}
			if len(names) != len(c.want) {
				t.Fatalf("tools = %v, want %v", names, c.want)
			}
			for i := range c.want {
				if names[i] != c.want[i] {
					t.Errorf("tools = %v, want %v", names, c.want)
				}
			}
		})
	}
}

func TestFileToolsRefuseOutsideATurn(t *testing.T) {
	a, _ := newAgent(t, say("x"))
	e := connect(t, a)
	e.initialize(t, fullCaps())
	for _, tool := range a.FileTools() {
		_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"/tmp/x","content":""}`))
		if err == nil {
			t.Errorf("%s ran outside a prompt turn", tool.Name())
		}
	}
}

func TestReadFileReportsTheEditorsError(t *testing.T) {
	a, _ := newAgent(t, say("x"))
	e := connect(t, a)
	e.initialize(t, fullCaps())
	id := e.newSession(t)
	var read golm.Tool
	for _, tool := range a.FileTools() {
		if tool.Name() == "read_file" {
			read = tool
		}
	}
	_, err := read.Execute(acp.SessionContext(context.Background(), id), json.RawMessage(`{"path":"/nope"}`))
	if err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Errorf("err = %v", err)
	}
}

func TestEmptyPathIsRefused(t *testing.T) {
	a, _ := newAgent(t, say("x"))
	e := connect(t, a)
	e.initialize(t, fullCaps())
	id := e.newSession(t)
	for _, tool := range a.FileTools() {
		if _, err := tool.Execute(acp.SessionContext(context.Background(), id),
			json.RawMessage(`{"path":"  ","content":"x"}`)); err == nil {
			t.Errorf("%s accepted an empty path", tool.Name())
		}
	}
}

func toolText(out []golm.ToolContent) string {
	var b strings.Builder
	for _, c := range out {
		if t, ok := c.(golm.Text); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

func runFileTool(t *testing.T, a *acp.Agent, id, name, args string) (string, error) {
	t.Helper()
	for _, tool := range a.FileTools() {
		if tool.Name() != name {
			continue
		}
		ctx := golm.WithToolCall(acp.SessionContext(context.Background(), id), "call_1")
		out, err := tool.Execute(ctx, json.RawMessage(args))
		return toolText(out), err
	}
	t.Fatalf("no %s tool", name)
	return "", nil
}

// A tool result is re-sent on every later step.
func TestReadFileIsBounded(t *testing.T) {
	a, _ := newAgent(t, say("x"))
	e := connect(t, a)
	e.initialize(t, fullCaps())
	big := strings.Repeat("x", 400<<10)
	e.peer.Handle(acp.MethodReadTextFile, func(context.Context, json.RawMessage) (any, error) {
		return acp.ReadTextFileResponse{Content: big}, nil
	})

	out, err := runFileTool(t, a, e.newSession(t), "read_file", `{"path":"/a.txt"}`)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(out) >= len(big) {
		t.Errorf("an unbounded %d-byte buffer reached the model", len(out))
	}
	if !strings.Contains(out, "truncated") {
		t.Error("the model was not told the file was cut")
	}
}

// Half a file written over a whole one is worse than not writing it.
func TestWriteFileRefusesAnOversizedBody(t *testing.T) {
	a, _ := newAgent(t, say("x"))
	e := connect(t, a)
	e.initialize(t, fullCaps())
	var wrote bool
	e.peer.Handle(acp.MethodWriteTextFile, func(context.Context, json.RawMessage) (any, error) {
		wrote = true
		return acp.WriteTextFileResponse{}, nil
	})

	args, _ := json.Marshal(map[string]string{"path": "/a.txt", "content": strings.Repeat("x", 5<<20)})
	if _, err := runFileTool(t, a, e.newSession(t), "write_file", string(args)); err == nil {
		t.Fatal("a 5 MiB write was accepted")
	}
	if wrote {
		t.Error("the editor was asked to write it anyway")
	}
}
