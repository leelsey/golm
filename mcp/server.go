// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/internal/jsonrpc"
	"github.com/leelsey/golm/internal/rpc"
)

// Server exposes golm tools and agents over MCP.
type Server struct {
	info     Implementation
	registry *golm.Registry
	rpc      *rpc.Server
	policy   golm.ToolPolicy
}

// NewServer returns a Server identifying itself as name/version.
func NewServer(name, version string) *Server {
	s := &Server{
		info:     Implementation{Name: name, Version: version},
		registry: golm.NewRegistry(),
		rpc:      rpc.NewServer(),
	}
	s.rpc.Handle("initialize", s.handleInitialize)
	s.rpc.Handle("notifications/initialized", func(context.Context, json.RawMessage) (any, error) { return nil, nil })
	s.rpc.Handle("ping", func(context.Context, json.RawMessage) (any, error) { return struct{}{}, nil })
	s.rpc.Handle("tools/list", s.handleListTools)
	s.rpc.Handle("tools/call", s.handleCallTool)
	return s
}

// AddTools exposes the given tools.
func (s *Server) AddTools(tools ...golm.Tool) { s.registry.Register(tools...) }

// AddRegistry exposes every tool in reg.
func (s *Server) AddRegistry(reg *golm.Registry) { s.registry.Register(reg.List()...) }

// AddAgent exposes an agent as a single tool that runs the agent on an "input" string.
func (s *Server) AddAgent(name, description string, a *golm.Agent) {
	s.registry.Register(golm.TextTool(name, description, "input", "",
		func(ctx context.Context, input string) (string, error) {
			res, err := a.Run(ctx, golm.NewSession(), input)
			if err != nil {
				return "", err
			}
			return res.Text(), nil
		}))
}

// WithPolicy gates every tools/call through p, the same decision point an Agent applies to its own loop.
func (s *Server) WithPolicy(p golm.ToolPolicy) *Server {
	s.policy = p
	return s
}

// Serve runs the MCP server over t until the transport closes.
func (s *Server) Serve(ctx context.Context, t rpc.Transport) error { return s.rpc.Serve(ctx, t) }

func (s *Server) handleInitialize(_ context.Context, params json.RawMessage) (any, error) {
	version := ProtocolVersion
	var p InitializeParams
	if json.Unmarshal(params, &p) == nil && versionSupported(p.ProtocolVersion) {
		version = p.ProtocolVersion
	}
	return InitializeResult{
		ProtocolVersion: version,
		Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
		ServerInfo:      s.info,
	}, nil
}

func (s *Server) handleListTools(_ context.Context, _ json.RawMessage) (any, error) {
	tools := s.registry.List()
	out := make([]Tool, 0, len(tools))
	for _, t := range tools {
		schema := t.Schema()
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		out = append(out, Tool{Name: t.Name(), Description: t.Description(), InputSchema: schema})
	}
	return ListToolsResult{Tools: out}, nil
}

func (s *Server) handleCallTool(ctx context.Context, params json.RawMessage) (any, error) {
	var p CallToolParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "invalid tools/call params")
	}
	tool, ok := s.registry.Get(p.Name)
	if !ok {
		return textResult(fmt.Sprintf("unknown tool %q", p.Name), true), nil
	}
	args := p.Arguments
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	ctx, err := s.decide(ctx, tool, p.Name, args)
	if err != nil {
		return textResult(fmt.Sprintf("tool %q was not run: %v", p.Name, err), true), nil
	}
	out, err := safeExecute(ctx, tool, args)
	if err != nil {
		return textResult(err.Error(), true), nil
	}
	return blockResult(out, false), nil
}

func (s *Server) decide(ctx context.Context, tool golm.Tool, name string, args json.RawMessage) (c context.Context, err error) {
	if s.policy == nil {
		return ctx, nil
	}
	defer func() {
		if r := recover(); r != nil {
			c, err = nil, fmt.Errorf("%w: policy panicked: %v", golm.ErrToolDenied, r)
		}
	}()
	c, err = s.policy(ctx, golm.ToolRequest{Tool: tool, Call: golm.ToolUse{Name: name, Input: args}})
	if err != nil {
		return nil, err
	}
	if c == nil {
		c = ctx
	}
	return c, nil
}

func safeExecute(ctx context.Context, tool golm.Tool, args json.RawMessage) (out []golm.ToolContent, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("tool panicked: %v", r)
		}
	}()
	return tool.Execute(ctx, args)
}
