// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package acp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/leelsey/golm"
)

const (
	maxReadBytes  = 256 << 10
	maxWriteBytes = 4 << 20
)

// FileTools are read_file and write_file answered by the EDITOR rather than by the filesystem.
func (a *Agent) FileTools() []golm.Tool {
	fs := a.clientFS()
	var out []golm.Tool
	if fs.ReadTextFile {
		out = append(out, a.readTool())
	}
	if fs.WriteTextFile {
		out = append(out, a.writeTool())
	}
	return out
}

type readArgs struct {
	Path  string `json:"path" jsonschema:"absolute path of the file to read"`
	Line  int    `json:"line,omitempty" jsonschema:"1-based line to start at; omit for the whole file"`
	Limit int    `json:"limit,omitempty" jsonschema:"how many lines to read from line"`
}

func (a *Agent) readTool() golm.Tool {
	t := golm.NewTypedTool("read_file",
		"Read a text file from the editor. This returns what the editor has, including unsaved changes, "+
			"so it is what the person is actually looking at.",
		func(ctx context.Context, in readArgs) (string, error) {
			req := ReadTextFileRequest{SessionID: SessionIDOf(ctx), Path: strings.TrimSpace(in.Path)}
			if req.Path == "" {
				return "", errors.New("path must not be empty")
			}
			if in.Line > 0 {
				req.Line = &in.Line
			}
			if in.Limit > 0 {
				req.Limit = &in.Limit
			}
			var resp ReadTextFileResponse
			if err := a.callClient(ctx, MethodReadTextFile, req, &resp); err != nil {
				return "", err
			}
			if len(resp.Content) > maxReadBytes {
				return truncate(resp.Content, maxReadBytes) +
					fmt.Sprintf("\n\n[truncated: %s is %d bytes]", req.Path, len(resp.Content)), nil
			}
			return resp.Content, nil
		})

	return golm.WithTraits(t, golm.ToolTraits{ReadOnly: true, Filesystem: true})
}

type writeArgs struct {
	Path    string `json:"path" jsonschema:"absolute path of the file to write"`
	Content string `json:"content" jsonschema:"the complete new contents of the file"`
}

func (a *Agent) writeTool() golm.Tool {
	t := golm.NewTypedTool("write_file",
		"Write a text file through the editor, replacing its contents. The change lands in the editor's "+
			"buffer, where it can be reviewed and undone.",
		func(ctx context.Context, in writeArgs) (string, error) {
			path := strings.TrimSpace(in.Path)
			if path == "" {
				return "", errors.New("path must not be empty")
			}

			if len(in.Content) > maxWriteBytes {
				return "", fmt.Errorf("%d bytes is over the write limit of %d", len(in.Content), maxWriteBytes)
			}
			var resp WriteTextFileResponse
			if err := a.callClient(ctx, MethodWriteTextFile, WriteTextFileRequest{
				SessionID: SessionIDOf(ctx), Path: path, Content: in.Content,
			}, &resp); err != nil {
				return "", err
			}
			return fmt.Sprintf("wrote %s (%d bytes)", path, len(in.Content)), nil
		})
	return golm.WithTraits(t, golm.ToolTraits{Filesystem: true})
}

func (a *Agent) callClient(ctx context.Context, method string, params, result any) error {
	if a.peer == nil {
		return errors.New("acp: not connected")
	}
	if SessionIDOf(ctx) == "" {
		return errors.New("acp: no session; this tool only works inside a prompt turn")
	}
	return a.peer.Call(ctx, method, params, result)
}
