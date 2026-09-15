// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package mcp

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/internal/proc"
	"github.com/leelsey/golm/internal/rpc"
)

const defaultCloseGrace = 5 * time.Second

// Client speaks MCP to a server.
type Client struct {
	CloseGrace time.Duration

	rpc *rpc.Client
	cmd *exec.Cmd
}

// NewClient wraps an existing transport.
func NewClient(t rpc.Transport) *Client { return &Client{rpc: rpc.NewClient(t)} }

// Dialer configures how a server subprocess is launched.
type Dialer struct {
	Stderr io.Writer

	CloseGrace time.Duration

	Env []string

	Inherit []string
}

// Dial launches command as a stdio MCP server subprocess.
func (d Dialer) Dial(ctx context.Context, command string, args ...string) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.Command(command, args...)
	cmd.Env = d.env()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if cmd.Stderr = d.Stderr; cmd.Stderr == nil {
		cmd.Stderr = os.Stderr
	}

	proc.Bound(cmd, cmp.Or(d.CloseGrace, defaultCloseGrace))
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	t := rpc.NewStreamTransport(stdout, stdin, stdin)

	return &Client{rpc: rpc.NewClient(t), cmd: cmd, CloseGrace: d.CloseGrace}, nil
}

// Dial launches command as a stdio MCP server subprocess, with the server's diagnostics going to os.Stderr.
func Dial(ctx context.Context, command string, args ...string) (*Client, error) {
	return Dialer{}.Dial(ctx, command, args...)
}

func (d Dialer) env() []string {
	if d.Env != nil {
		return d.Env
	}
	return proc.MinimalEnv(d.Inherit)
}

// Live reports whether the connection is still usable.
func (c *Client) Live() bool { return c.rpc != nil && c.rpc.Live() }

// Initialize performs the MCP handshake.
func (c *Client) Initialize(ctx context.Context, clientName string) error {
	var res InitializeResult
	err := c.rpc.Call(ctx, "initialize", InitializeParams{
		ProtocolVersion: ProtocolVersion,
		ClientInfo:      Implementation{Name: clientName, Version: golm.Version},
	}, &res)
	if err != nil {
		return err
	}

	return c.rpc.Notify(ctx, "notifications/initialized", nil)
}

// ListTools returns the server's advertised tools.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	var all []Tool
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var res ListToolsResult
		if err := c.rpc.Call(ctx, "tools/list", params, &res); err != nil {
			return nil, err
		}
		all = append(all, res.Tools...)
		if res.NextCursor == "" || res.NextCursor == cursor {
			return all, nil
		}
		cursor = res.NextCursor
	}
}

// CallTool invokes a tool, returning its concatenated text content.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (string, bool, error) {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	} else if !json.Valid(args) {
		return "", false, fmt.Errorf("mcp: arguments for tool %q are not valid JSON", name)
	}
	var res CallToolResult
	if err := c.rpc.Call(ctx, "tools/call", CallToolParams{Name: name, Arguments: args}, &res); err != nil {
		return "", false, err
	}
	var sb strings.Builder
	for _, blk := range res.Content {
		switch blk.Type {
		case "text", "":
			sb.WriteString(blk.Text)
		default:

			if blk.MimeType != "" {
				fmt.Fprintf(&sb, "[%s %s]", blk.Type, blk.MimeType)
			} else {
				fmt.Fprintf(&sb, "[%s]", blk.Type)
			}
		}
	}
	return sb.String(), res.IsError, nil
}

// Tools returns the server's tools wrapped as golm.Tool values.
func (c *Client) Tools(ctx context.Context) ([]golm.Tool, error) {
	list, err := c.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]golm.Tool, 0, len(list))
	for _, t := range list {
		name := t.Name
		out = append(out, golm.ToolFunc{
			NameVal:        name,
			DescriptionVal: t.Description,
			SchemaVal:      t.InputSchema,
			Fn: func(ctx context.Context, input json.RawMessage) ([]golm.ToolContent, error) {
				text, isErr, err := c.CallTool(ctx, name, input)
				if err != nil {
					return nil, err
				}
				if isErr {
					return nil, fmt.Errorf("%s", text)
				}
				return golm.ToolText(text), nil
			},
		})
	}
	return out, nil
}

// Close shuts down the client.
func (c *Client) Close() error {
	err := c.rpc.Close()
	if c.cmd != nil {
		grace := cmp.Or(c.CloseGrace, defaultCloseGrace)
		done := make(chan struct{})
		go func() { _ = c.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(grace):
		}

		_ = proc.KillGroup(c.cmd)
		select {
		case <-done:
		case <-time.After(grace):
		}
	}
	return err
}
