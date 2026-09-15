// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package acp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/internal/jsonrpc"
)

func (a *Agent) handlePrompt(ctx context.Context, params json.RawMessage) (any, error) {
	var req PromptRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, badParams(err)
	}
	s := a.session(req.SessionID)
	if s == nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "no session "+req.SessionID)
	}
	msg, err := promptMessage(req.Prompt)
	if err != nil {
		return nil, badParams(err)
	}

	turnCtx, cancel := context.WithCancel(ctx)
	if !s.claim(cancel) {
		cancel()
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidRequest,
			"a turn is already running in session "+req.SessionID+"; cancel it before starting another")
	}
	defer func() {
		cancel()
		s.finish()
	}()

	turnCtx = withSessionID(turnCtx, req.SessionID)

	res, runErr := a.Runner.StreamMessage(turnCtx, s.sess, msg, a.streamTo(turnCtx, req.SessionID))
	a.save(ctx, s.sess)

	if runErr != nil && !isCancellation(runErr) {
		if reason := stopReasonOf(res, runErr); reason != StopEndTurn {
			a.notifyRunProblem(ctx, req.SessionID, runErr)
			return PromptResponse{StopReason: reason}, nil
		}
		return nil, runErr
	}
	return PromptResponse{StopReason: stopReasonOf(res, runErr)}, nil
}

func isCancellation(err error) bool { return errors.Is(err, context.Canceled) }

func (a *Agent) notifyRunProblem(ctx context.Context, sessionID string, err error) {
	a.notify(ctx, sessionID, AgentMessage("\n\n["+err.Error()+"]"))
}

func (a *Agent) streamTo(ctx context.Context, sessionID string) func(golm.StreamEvent) error {
	var mu sync.Mutex
	announced := map[string]bool{}
	announce := func(id string) bool {
		mu.Lock()
		defer mu.Unlock()
		if announced[id] {
			return false
		}
		announced[id] = true
		return true
	}
	seen := func(id string) bool {
		mu.Lock()
		defer mu.Unlock()
		return announced[id]
	}

	return func(ev golm.StreamEvent) error {
		switch ev.Type {
		case golm.EventTextDelta:
			if ev.Text == "" {
				return nil
			}
			if ev.Depth > 0 {
				a.notify(ctx, sessionID, AgentThought(ev.Text))
				return nil
			}
			a.notify(ctx, sessionID, AgentMessage(ev.Text))
		case golm.EventThinkingDelta:
			if ev.Text != "" {
				a.notify(ctx, sessionID, AgentThought(ev.Text))
			}
		case golm.EventToolStart:
			if !announce(ev.ToolID) {
				return nil
			}
			a.notify(ctx, sessionID, ToolCallStart{
				SessionUpdate: UpdateToolCall,
				ToolCallID:    ev.ToolID,
				Title:         ev.ToolName,
				Kind:          KindOf(ev.ToolName),
				Status:        StatusPending,
			})
		case golm.EventToolDelta:
			if ev.ToolArgs == "" {
				return nil
			}
			a.notify(ctx, sessionID, ToolCallStart{
				SessionUpdate: UpdateToolCallUpdate,
				ToolCallID:    ev.ToolID,
				Status:        StatusInProgress,
				RawInput:      json.RawMessage(ev.ToolArgs),
			})
		case golm.EventToolStop:

			if !seen(ev.ToolID) {
				return nil
			}
			a.notify(ctx, sessionID, ToolCallProgress{
				SessionUpdate: UpdateToolCallUpdate,
				ToolCallID:    ev.ToolID,
				Status:        StatusInProgress,
			})
		case golm.EventToolResult:
			status := StatusCompleted
			if ev.ToolError {
				status = StatusFailed
			}

			if announce(ev.ToolID) {
				a.notify(ctx, sessionID, ToolCallStart{
					SessionUpdate: UpdateToolCall,
					ToolCallID:    ev.ToolID,
					Title:         ev.ToolName,
					Kind:          KindOf(ev.ToolName),
					Status:        StatusInProgress,
				})
			}
			a.notify(ctx, sessionID, ToolCallProgress{
				SessionUpdate: UpdateToolCallUpdate,
				ToolCallID:    ev.ToolID,
				Status:        status,
				Content:       ToolOutput(truncate(ev.Text, maxToolOutput)),
			})
		case golm.EventAgentStart:

			a.notify(ctx, sessionID, ToolCallStart{
				SessionUpdate: UpdateToolCall,
				ToolCallID:    "agent_" + ev.Agent,
				Title:         "delegating to " + ev.Agent,
				Kind:          KindThink,
				Status:        StatusInProgress,
			})
		case golm.EventAgentStop:
			a.notify(ctx, sessionID, ToolCallProgress{
				SessionUpdate: UpdateToolCallUpdate,
				ToolCallID:    "agent_" + ev.Agent,
				Status:        StatusCompleted,
			})
		}
		return nil
	}
}

const maxToolOutput = 4 << 10

// KindOf classifies a tool for the editor.
func KindOf(tool string) string {
	name := strings.ToLower(tool)
	switch {
	case strings.HasPrefix(name, "agent_"):
		return KindThink
	case strings.Contains(name, "search") || strings.Contains(name, "grep") || strings.Contains(name, "find"):
		return KindSearch
	case strings.Contains(name, "fetch") || strings.Contains(name, "http") || strings.Contains(name, "web"):
		return KindFetch
	case strings.Contains(name, "run") || strings.Contains(name, "exec") || strings.Contains(name, "shell") || strings.Contains(name, "bash"):
		return KindExecute
	case strings.Contains(name, "delete") || strings.Contains(name, "remove") || strings.Contains(name, "rm_"):
		return KindDelete
	case strings.Contains(name, "move") || strings.Contains(name, "rename"):
		return KindMove
	case strings.Contains(name, "write") || strings.Contains(name, "edit") || strings.Contains(name, "create") ||
		strings.Contains(name, "remember") || strings.Contains(name, "forget"):
		return KindEdit
	case strings.Contains(name, "read") || strings.Contains(name, "list") || strings.Contains(name, "cat") ||
		strings.Contains(name, "skill"):
		return KindRead
	case strings.Contains(name, "think") || strings.Contains(name, "plan"):
		return KindThink
	}
	return KindOther
}

func (a *Agent) save(ctx context.Context, sess *golm.Session) {
	if a.Store == nil {
		return
	}
	var err error
	if a.Orchestrator != nil {
		err = a.Orchestrator.SaveConversation(ctx, a.Store, sess)
	} else {
		err = a.Store.Save(ctx, sess)
	}
	if err != nil {
		a.log(ctx, slog.LevelWarn, "acp: session not saved", "session", sess.ID(), "error", err)
	}
}

type sessionIDKey struct{}

func withSessionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, sessionIDKey{}, id)
}

// SessionIDOf returns the ACP session a run belongs to, or "" outside one.
func SessionIDOf(ctx context.Context) string {
	id, _ := ctx.Value(sessionIDKey{}).(string)
	return id
}
