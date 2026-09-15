// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

// Package a2a implements the Agent2Agent.
package a2a

import (
	"encoding/json"
	"strings"
)

const wellKnownPath = "/.well-known/agent-card.json"

const (
	codeTaskNotFound      = -32001
	codeTaskNotCancelable = -32002
)

// TaskState enumerates the A2A task lifecycle states.
type TaskState string

const (
	StateSubmitted     TaskState = "submitted"
	StateWorking       TaskState = "working"
	StateInputRequired TaskState = "input-required"
	StateAuthRequired  TaskState = "auth-required"
	StateCompleted     TaskState = "completed"
	StateFailed        TaskState = "failed"
	StateCanceled      TaskState = "canceled"
	StateRejected      TaskState = "rejected"
)

// AgentCard describes an A2A agent, served at the well-known path.
type AgentCard struct {
	Name               string       `json:"name"`
	Description        string       `json:"description"`
	Version            string       `json:"version"`
	URL                string       `json:"url"`
	Capabilities       Capabilities `json:"capabilities"`
	Skills             []Skill      `json:"skills"`
	DefaultInputModes  []string     `json:"defaultInputModes,omitempty"`
	DefaultOutputModes []string     `json:"defaultOutputModes,omitempty"`
}

// Capabilities advertises optional A2A features.
type Capabilities struct {
	Streaming         bool `json:"streaming"`
	PushNotifications bool `json:"pushNotifications"`
}

// Skill is one callable capability advertised by an agent.
type Skill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	InputModes  []string `json:"inputModes,omitempty"`
	OutputModes []string `json:"outputModes,omitempty"`
}

// FilePart is a file content part.
type FilePart struct {
	Name     string `json:"name,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	URI      string `json:"uri,omitempty"`
	Bytes    string `json:"bytes,omitempty"`
}

// Part is a content part: exactly one of Text, File or Data is set.
type Part struct {
	Text string
	File *FilePart
	Data json.RawMessage
}

// TextPart builds a text Part.
func TextPart(s string) Part { return Part{Text: s} }

// MarshalJSON encodes the part in A2A union form.
func (p Part) MarshalJSON() ([]byte, error) {
	switch {
	case p.File != nil:
		return json.Marshal(struct {
			Kind string    `json:"kind"`
			File *FilePart `json:"file"`
		}{"file", p.File})
	case len(p.Data) > 0:
		return json.Marshal(struct {
			Kind string          `json:"kind"`
			Data json.RawMessage `json:"data"`
		}{"data", p.Data})
	default:
		return json.Marshal(struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		}{"text", p.Text})
	}
}

// UnmarshalJSON decodes a part, tolerating both tagged and untagged forms.
func (p *Part) UnmarshalJSON(b []byte) error {
	var raw struct {
		Kind string          `json:"kind"`
		Text string          `json:"text"`
		File *FilePart       `json:"file"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	switch raw.Kind {
	case "file":
		p.File = raw.File
	case "data":
		p.Data = raw.Data
	case "text":
		p.Text = raw.Text
	default:
		switch {
		case raw.File != nil:
			p.File = raw.File
		case len(raw.Data) > 0:
			p.Data = raw.Data
		default:
			p.Text = raw.Text
		}
	}
	return nil
}

func isTerminal(s TaskState) bool {
	switch s {
	case StateCompleted, StateFailed, StateCanceled, StateRejected:
		return true
	}
	return false
}

// Message is an A2A message turn.
type Message struct {
	Role      string `json:"role"`
	Parts     []Part `json:"parts"`
	MessageID string `json:"messageId,omitempty"`
	TaskID    string `json:"taskId,omitempty"`
	ContextID string `json:"contextId,omitempty"`
	Kind      string `json:"kind,omitempty"`
}

// Text returns the concatenated text of the message's text parts.
func (m Message) Text() string {
	var b strings.Builder
	for _, p := range m.Parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

// StreamAssembler accumulates the text of a message/stream.
type StreamAssembler struct {
	full     strings.Builder
	final    string
	sawDelta bool
	sawFinal bool
}

// Delta records an incremental chunk.
func (a *StreamAssembler) Delta(text string, appendChunk bool) {
	if !appendChunk {
		a.full.Reset()
	}
	a.full.WriteString(text)
	a.sawDelta = true
}

// Final records an authoritative final text.
func (a *StreamAssembler) Final(text string) { a.final = text; a.sawFinal = true }

// Result prefers the final text over accumulated deltas.
func (a *StreamAssembler) Result() string {
	if a.sawFinal && a.final != "" {
		return a.final
	}
	if a.sawDelta {
		return a.full.String()
	}
	return a.final
}

// TaskStatus is a task's current state.
type TaskStatus struct {
	State     TaskState `json:"state"`
	Message   *Message  `json:"message,omitempty"`
	Timestamp string    `json:"timestamp,omitempty"`
}

// Artifact is an output produced by a task.
type Artifact struct {
	ArtifactID  string `json:"artifactId"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Parts       []Part `json:"parts"`
}

// Task is the unit of work in A2A.
type Task struct {
	ID        string     `json:"id"`
	ContextID string     `json:"contextId,omitempty"`
	Status    TaskStatus `json:"status"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
	History   []Message  `json:"history,omitempty"`
	Kind      string     `json:"kind,omitempty"`
}

// MessageSendParams is the params object for message/send and message/stream.
type MessageSendParams struct {
	Message Message `json:"message"`
}

// TaskQueryParams is the params object for tasks/get and tasks/cancel.
type TaskQueryParams struct {
	ID string `json:"id"`
}

// TaskStatusUpdateEvent is streamed on state transitions.
type TaskStatusUpdateEvent struct {
	TaskID    string     `json:"taskId"`
	ContextID string     `json:"contextId,omitempty"`
	Status    TaskStatus `json:"status"`
	Final     bool       `json:"final"`
	Kind      string     `json:"kind"`
}

// TaskArtifactUpdateEvent is streamed as artifacts are produced.
type TaskArtifactUpdateEvent struct {
	TaskID    string   `json:"taskId"`
	ContextID string   `json:"contextId,omitempty"`
	Artifact  Artifact `json:"artifact"`
	Append    bool     `json:"append,omitempty"`
	LastChunk bool     `json:"lastChunk,omitempty"`
	Kind      string   `json:"kind"`
}
