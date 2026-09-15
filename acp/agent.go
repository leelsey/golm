// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package acp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/internal/jsonrpc"
	"github.com/leelsey/golm/internal/rpc"
)

// Agent serves a golm.Runner over ACP.
type Agent struct {
	Runner golm.Runner

	Store golm.SessionStore

	Orchestrator *golm.Orchestrator

	Info Implementation

	Logger *slog.Logger

	MaxSessions int

	OnInitialized func()

	peer *rpc.Peer

	mu       sync.Mutex
	sessions map[string]*acpSession

	order []string

	clientCaps ClientCapabilities
	negotiated bool
}

// DefaultMaxSessions bounds an agent's conversations when MaxSessions is unset.
const DefaultMaxSessions = 64

type acpSession struct {
	sess *golm.Session
	cwd  string
	mu   sync.Mutex

	cancel context.CancelFunc
}

func (s *acpSession) claim(cancel context.CancelFunc) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return false
	}
	s.cancel = cancel
	return true
}

func (s *acpSession) finish() {
	s.mu.Lock()
	s.cancel = nil
	s.mu.Unlock()
}

func (s *acpSession) stop() bool {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

// NewAgent returns an Agent serving r.
func NewAgent(r golm.Runner) *Agent {
	return &Agent{
		Runner:   r,
		Info:     Implementation{Name: "golm", Title: "golm", Version: golm.Version},
		sessions: map[string]*acpSession{},
	}
}

// Serve runs the protocol over t until the connection ends.
func (a *Agent) Serve(ctx context.Context, t rpc.Transport) error {
	if a.Runner == nil {
		return errors.New("acp: no Runner")
	}
	if a.sessions == nil {
		a.sessions = map[string]*acpSession{}
	}
	p := rpc.NewPeer(t)
	a.peer = p
	p.Handle(MethodInitialize, a.handleInitialize)
	p.Handle(MethodAuthenticate, a.handleAuthenticate)
	p.Handle(MethodNewSession, a.handleNewSession)
	p.Handle(MethodLoadSession, a.handleLoadSession)
	p.Handle(MethodPrompt, a.handlePrompt)
	p.Handle(MethodCancel, a.handleCancel)
	return p.Serve(ctx)
}

func (a *Agent) log(ctx context.Context, level slog.Level, msg string, attrs ...any) {
	if a.Logger != nil {
		a.Logger.Log(ctx, level, msg, attrs...)
	}
}

func badParams(err error) error {
	return jsonrpc.Errorf(jsonrpc.CodeInvalidParams, err.Error())
}

func (a *Agent) handleInitialize(ctx context.Context, params json.RawMessage) (any, error) {
	var req InitializeRequest
	if len(params) > 0 {
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, badParams(err)
		}
	}
	a.mu.Lock()
	a.clientCaps = req.ClientCapabilities
	first := !a.negotiated
	a.negotiated = true
	hook := a.OnInitialized
	a.mu.Unlock()
	if first && hook != nil {
		hook()
	}

	a.log(ctx, slog.LevelInfo, "acp: initialised",
		"client_version", req.ProtocolVersion, "fs_read", req.ClientCapabilities.FS.ReadTextFile,
		"fs_write", req.ClientCapabilities.FS.WriteTextFile)

	return InitializeResponse{
		ProtocolVersion: Version,
		AgentInfo:       &a.Info,
		AgentCapabilities: AgentCapabilities{
			LoadSession: a.Store != nil,
			PromptCapabilities: PromptCapabilities{
				Image: true, Audio: true, EmbeddedContext: true,
			},
		},

		AuthMethods: []AuthMethod{},
	}, nil
}

func (a *Agent) handleAuthenticate(context.Context, json.RawMessage) (any, error) {
	return map[string]any{}, nil
}

func newSessionID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "sess_" + base64.RawURLEncoding.EncodeToString(b)
}

func (a *Agent) handleNewSession(ctx context.Context, params json.RawMessage) (any, error) {
	var req NewSessionRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, badParams(err)
	}

	if len(req.MCPServers) > 0 {
		a.log(ctx, slog.LevelWarn, "acp: ignoring client-supplied MCP servers; golm connects the ones in its own config",
			"count", len(req.MCPServers))
	}
	id := newSessionID()
	a.remember(id, &acpSession{sess: golm.SessionData{ID: id}.Session(), cwd: req.CWD})
	a.log(ctx, slog.LevelInfo, "acp: session opened", "session", id, "cwd", req.CWD)
	return NewSessionResponse{SessionID: id}, nil
}

func (a *Agent) handleLoadSession(ctx context.Context, params json.RawMessage) (any, error) {
	var req LoadSessionRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, badParams(err)
	}
	if a.Store == nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidRequest,
			"this agent has no session store, so nothing can be loaded")
	}
	sess, err := a.Store.Load(ctx, req.SessionID)
	if err != nil {
		if errors.Is(err, golm.ErrSessionNotFound) {
			return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "no session "+req.SessionID)
		}
		return nil, err
	}
	if a.Orchestrator != nil {
		if _, err := a.Orchestrator.LoadConversation(ctx, a.Store, sess); err != nil {
			return nil, err
		}
	}

	if old := a.session(req.SessionID); old != nil {
		old.mu.Lock()
		running := old.cancel != nil
		old.mu.Unlock()
		if running {
			return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidRequest,
				"a turn is already running in session "+req.SessionID+"; cancel it before reloading")
		}
	}
	a.remember(req.SessionID, &acpSession{sess: sess, cwd: req.CWD})

	for _, m := range sess.History() {
		text := m.Text()
		if strings.TrimSpace(text) == "" {
			continue
		}
		switch m.Role {
		case golm.RoleUser:
			a.notify(ctx, req.SessionID, UserMessage(text))
		case golm.RoleAssistant:
			a.notify(ctx, req.SessionID, AgentMessage(text))
		}
	}
	a.log(ctx, slog.LevelInfo, "acp: session loaded", "session", req.SessionID, "messages", sess.Len())
	return LoadSessionResponse{}, nil
}

func (a *Agent) remember(id string, s *acpSession) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sessions == nil {
		a.sessions = map[string]*acpSession{}
	}
	if _, exists := a.sessions[id]; !exists {
		a.order = append(a.order, id)
	}
	a.sessions[id] = s
	limit := a.MaxSessions
	if limit <= 0 {
		limit = DefaultMaxSessions
	}
	for len(a.order) > limit {
		oldest := a.order[0]
		a.order = a.order[1:]

		if old := a.sessions[oldest]; old != nil {
			old.mu.Lock()
			running := old.cancel != nil
			old.mu.Unlock()
			if running {
				a.order = append(a.order, oldest)
				continue
			}
		}
		delete(a.sessions, oldest)
	}
}

func (a *Agent) session(id string) *acpSession {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessions[id]
}

func (a *Agent) handleCancel(ctx context.Context, params json.RawMessage) (any, error) {
	var req CancelNotification
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, nil
	}
	s := a.session(req.SessionID)
	if s == nil {
		return nil, nil
	}
	if s.stop() {
		a.log(ctx, slog.LevelInfo, "acp: turn cancelled", "session", req.SessionID)
	}
	return nil, nil
}

func (a *Agent) notify(ctx context.Context, sessionID string, update any) {
	if a.peer == nil {
		return
	}
	_ = a.peer.Notify(ctx, MethodSessionUpdate, SessionNotification{
		SessionID: sessionID, Update: update,
	})
}

func (a *Agent) clientFS() FileSystemCapabilities {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.clientCaps.FS
}

func stopReasonOf(res golm.Result, err error) StopReason {
	switch {
	case errors.Is(err, context.Canceled):
		return StopCancelled
	case errors.Is(err, golm.ErrRefused):
		return StopRefusal
	case errors.Is(err, golm.ErrMaxSteps):
		return StopMaxTurnRequests
	}
	switch res.StopReason {
	case golm.StopMaxTokens:
		return StopMaxTokens
	case golm.StopRefusal:
		return StopRefusal
	default:
		return StopEndTurn
	}
}

func promptMessage(blocks []ContentBlock) (golm.Message, error) {
	msg := golm.Message{Role: golm.RoleUser}
	var text strings.Builder
	for _, b := range blocks {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "resource":
			if b.Resource == nil {
				continue
			}
			fmt.Fprintf(&text, "\n\n<file uri=%q>\n%s\n</file>\n", b.Resource.URI, b.Resource.Text)
		case "resource_link":
			fmt.Fprintf(&text, "\n\n[%s](%s)", b.Name, b.URI)
		case "image":
			data, err := base64.StdEncoding.DecodeString(b.Data)
			if err != nil {
				return golm.Message{}, fmt.Errorf("image is not valid base64: %w", err)
			}
			msg.Content = append(msg.Content, golm.Image{Data: data, MediaType: b.MimeType})
		case "audio":
			data, err := base64.StdEncoding.DecodeString(b.Data)
			if err != nil {
				return golm.Message{}, fmt.Errorf("audio is not valid base64: %w", err)
			}
			msg.Content = append(msg.Content, golm.Audio{Data: data, MediaType: b.MimeType})
		default:
			return golm.Message{}, fmt.Errorf("unsupported content block %q", b.Type)
		}
	}
	if t := strings.TrimSpace(text.String()); t != "" {
		msg.Content = append([]golm.Content{golm.Text{Text: t}}, msg.Content...)
	}
	if len(msg.Content) == 0 {
		return golm.Message{}, errors.New("the prompt is empty")
	}
	return msg, nil
}

// SessionContext marks ctx as belonging to an ACP session.
func SessionContext(ctx context.Context, sessionID string) context.Context {
	return withSessionID(ctx, sessionID)
}
