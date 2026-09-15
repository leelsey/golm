// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

// Package acp serves a golm agent over the Agent Client Protocol.
package acp

import (
	"encoding/json"
)

// Version is the protocol version this implements.
const Version = 1

// Method names. Client-to-agent above, agent-to-client below.
const (
	MethodInitialize   = "initialize"
	MethodAuthenticate = "authenticate"
	MethodNewSession   = "session/new"
	MethodLoadSession  = "session/load"
	MethodPrompt       = "session/prompt"
	MethodCancel       = "session/cancel"

	MethodSessionUpdate     = "session/update"
	MethodRequestPermission = "session/request_permission"
	MethodReadTextFile      = "fs/read_text_file"
	MethodWriteTextFile     = "fs/write_text_file"
)

// Implementation names one side of the connection.
type Implementation struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

// InitializeRequest is the client's opening call.
type InitializeRequest struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientInfo         *Implementation    `json:"clientInfo,omitempty"`
	ClientCapabilities ClientCapabilities `json:"clientCapabilities"`
}

// ClientCapabilities is what the EDITOR can do for the agent.
type ClientCapabilities struct {
	FS       FileSystemCapabilities `json:"fs"`
	Terminal bool                   `json:"terminal,omitempty"`
}

// FileSystemCapabilities is what the editor says it can do with files.
type FileSystemCapabilities struct {
	ReadTextFile  bool `json:"readTextFile,omitempty"`
	WriteTextFile bool `json:"writeTextFile,omitempty"`
}

// InitializeResponse is what the agent answers with.
type InitializeResponse struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentInfo         *Implementation   `json:"agentInfo,omitempty"`
	AgentCapabilities AgentCapabilities `json:"agentCapabilities"`
	AuthMethods       []AuthMethod      `json:"authMethods"`
}

// AgentCapabilities is what this agent supports.
type AgentCapabilities struct {
	LoadSession        bool               `json:"loadSession"`
	PromptCapabilities PromptCapabilities `json:"promptCapabilities"`
}

// PromptCapabilities is what the editor can put INTO a prompt.
type PromptCapabilities struct {
	Image           bool `json:"image"`
	Audio           bool `json:"audio"`
	EmbeddedContext bool `json:"embeddedContext"`
}

// AuthMethod is a way to authenticate.
type AuthMethod struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// NewSessionRequest opens a conversation, rooted at a working directory.
type NewSessionRequest struct {
	CWD                   string      `json:"cwd"`
	MCPServers            []MCPServer `json:"mcpServers"`
	AdditionalDirectories []string    `json:"additionalDirectories,omitempty"`
}

// MCPServer is an MCP server the CLIENT asks the agent to connect to.
type MCPServer struct {
	Name    string   `json:"name"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	Env     []EnvVar `json:"env,omitempty"`

	Type string `json:"type,omitempty"`
	URL  string `json:"url,omitempty"`
}

// EnvVar is one variable for an MCP server the editor asked the agent to run.
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// NewSessionResponse carries the id every later call names.
type NewSessionResponse struct {
	SessionID string `json:"sessionId"`
}

// LoadSessionRequest resumes a conversation the agent stored earlier.
type LoadSessionRequest struct {
	SessionID             string      `json:"sessionId"`
	CWD                   string      `json:"cwd"`
	MCPServers            []MCPServer `json:"mcpServers"`
	AdditionalDirectories []string    `json:"additionalDirectories,omitempty"`
}

// LoadSessionResponse is empty.
type LoadSessionResponse struct{}

// PromptRequest is one turn.
type PromptRequest struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
}

// PromptResponse says why the turn ended.
type PromptResponse struct {
	StopReason StopReason `json:"stopReason"`
}

// StopReason is the protocol's vocabulary for the end of a turn.
type StopReason string

const (
	StopEndTurn         StopReason = "end_turn"
	StopMaxTokens       StopReason = "max_tokens"
	StopMaxTurnRequests StopReason = "max_turn_requests"
	StopRefusal         StopReason = "refusal"
	StopCancelled       StopReason = "cancelled"
)

// CancelNotification asks the agent to abandon the turn in flight.
type CancelNotification struct {
	SessionID string `json:"sessionId"`
}

// ContentBlock is one piece of a prompt or an update.
type ContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	URI      string `json:"uri,omitempty"`
	Name     string `json:"name,omitempty"`

	Resource *EmbeddedResource `json:"resource,omitempty"`
}

// EmbeddedResource is a document the editor has already read.
type EmbeddedResource struct {
	URI      string `json:"uri,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// TextBlock is the ordinary case.
func TextBlock(text string) ContentBlock { return ContentBlock{Type: "text", Text: text} }

// SessionNotification is one session/update.
type SessionNotification struct {
	SessionID string `json:"sessionId"`
	Update    any    `json:"update"`
}

// MessageChunk is a piece of the agent's answer, its thinking, or the user's turn echoed back.
type MessageChunk struct {
	SessionUpdate string       `json:"sessionUpdate"`
	Content       ContentBlock `json:"content"`
}

// ToolCallStart announces a call before it runs.
type ToolCallStart struct {
	SessionUpdate string          `json:"sessionUpdate"`
	ToolCallID    string          `json:"toolCallId"`
	Title         string          `json:"title"`
	Kind          string          `json:"kind"`
	Status        string          `json:"status"`
	RawInput      json.RawMessage `json:"rawInput,omitempty"`
}

// ToolCallProgress reports what became of it.
type ToolCallProgress struct {
	SessionUpdate string            `json:"sessionUpdate"`
	ToolCallID    string            `json:"toolCallId"`
	Status        string            `json:"status"`
	Content       []ToolCallContent `json:"content,omitempty"`
}

// AgentMessage, AgentThought and UserMessage build the chunk variants.
func AgentMessage(text string) MessageChunk {
	return MessageChunk{SessionUpdate: UpdateAgentMessageChunk, Content: TextBlock(text)}
}

func AgentThought(text string) MessageChunk {
	return MessageChunk{SessionUpdate: UpdateAgentThoughtChunk, Content: TextBlock(text)}
}

func UserMessage(text string) MessageChunk {
	return MessageChunk{SessionUpdate: UpdateUserMessageChunk, Content: TextBlock(text)}
}

// ToolOutput wraps a tool's text result for a ToolCallProgress.
func ToolOutput(text string) []ToolCallContent {
	if text == "" {
		return nil
	}
	block := TextBlock(text)
	return []ToolCallContent{{Type: "content", Content: &block}}
}

// Update variants.
const (
	UpdateAgentMessageChunk = "agent_message_chunk"
	UpdateAgentThoughtChunk = "agent_thought_chunk"
	UpdateUserMessageChunk  = "user_message_chunk"
	UpdateToolCall          = "tool_call"
	UpdateToolCallUpdate    = "tool_call_update"
)

// Tool-call status values.
const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
)

// Tool-call kinds. The editor renders each differently.
const (
	KindRead    = "read"
	KindEdit    = "edit"
	KindDelete  = "delete"
	KindMove    = "move"
	KindSearch  = "search"
	KindExecute = "execute"
	KindThink   = "think"
	KindFetch   = "fetch"
	KindOther   = "other"
)

// ToolCallContent is one piece of a tool's output.
type ToolCallContent struct {
	Type    string        `json:"type"`
	Content *ContentBlock `json:"content,omitempty"`

	TerminalID string `json:"terminalId,omitempty"`
}

// RequestPermissionRequest asks the person in the editor to approve a call.
type RequestPermissionRequest struct {
	SessionID string             `json:"sessionId"`
	ToolCall  ToolCallRef        `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

// ToolCallRef identifies the call being asked about.
type ToolCallRef struct {
	ToolCallID string          `json:"toolCallId"`
	Title      string          `json:"title,omitempty"`
	Kind       string          `json:"kind,omitempty"`
	Status     string          `json:"status,omitempty"`
	RawInput   json.RawMessage `json:"rawInput,omitempty"`
}

// PermissionOption is one answer the editor may offer.
type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

// Permission option kinds.
const (
	PermAllowOnce    = "allow_once"
	PermAllowAlways  = "allow_always"
	PermRejectOnce   = "reject_once"
	PermRejectAlways = "reject_always"
)

// RequestPermissionResponse is the person's answer to a gated tool call.
type RequestPermissionResponse struct {
	Outcome PermissionOutcome `json:"outcome"`
}

// PermissionOutcome is either a selection or a cancellation.
type PermissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}

const (
	OutcomeSelected  = "selected"
	OutcomeCancelled = "cancelled"
)

// ReadTextFileRequest asks the EDITOR for a file's contents.
type ReadTextFileRequest struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
	Line      *int   `json:"line,omitempty"`
	Limit     *int   `json:"limit,omitempty"`
}

// ReadTextFileResponse carries the editor's BUFFER, unsaved changes included.
type ReadTextFileResponse struct {
	Content string `json:"content"`
}

// WriteTextFileRequest hands the editor a file to write.
type WriteTextFileRequest struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
	Content   string `json:"content"`
}

// WriteTextFileResponse is empty.
type WriteTextFileResponse struct{}
