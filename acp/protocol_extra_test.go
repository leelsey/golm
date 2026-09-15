// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package acp_test

import (
	"encoding/json"
	"testing"

	"github.com/leelsey/golm/acp"
)

// The editor renders a thought differently from a reply.
func TestAgentThoughtIsTaggedAsAThought(t *testing.T) {
	th := acp.AgentThought("weighing two options")
	if th.SessionUpdate != acp.UpdateAgentThoughtChunk {
		t.Errorf("sessionUpdate = %q, want %q", th.SessionUpdate, acp.UpdateAgentThoughtChunk)
	}
	if th.SessionUpdate == acp.AgentMessage("x").SessionUpdate {
		t.Error("a thought is indistinguishable from a reply")
	}
	b, err := json.Marshal(th)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round map[string]any
	if err := json.Unmarshal(b, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if round["sessionUpdate"] != acp.UpdateAgentThoughtChunk {
		t.Errorf("on the wire: %s", b)
	}
}

// Ended is what the terminal polling loop asks to decide whether to keep waiting.
func TestTerminalOutputEndedTracksTheExitStatus(t *testing.T) {
	var running acp.TerminalOutputResponse
	if running.Ended() {
		t.Error("a command with no exit status reported as ended")
	}

	code := 0
	done := acp.TerminalOutputResponse{ExitStatus: &acp.TerminalExitStatus{ExitCode: &code}}
	if !done.Ended() {
		t.Error("a command carrying an exit status reported as still running")
	}

	var absent *acp.TerminalOutputResponse
	if absent.Ended() {
		t.Error("a nil response reported as ended")
	}
}
