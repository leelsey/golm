// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

// Package clibackend implements golm.Provider by shelling out to an external agentic CLI.
package clibackend

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/leelsey/golm"
)

var argPlaceholderMissWarned sync.Once

// PromptVia selects how the rendered prompt is delivered to the CLI.
type PromptVia string

const (
	PromptViaStdin PromptVia = "stdin"
	PromptViaArg   PromptVia = "arg"
)

// Config describes how to invoke an external agentic CLI.
type Config struct {
	Name        string
	Command     string
	Args        []string
	Via         PromptVia
	Placeholder string
}

// Client is a CLI-backed provider.
type Client struct {
	cfg Config
}

var _ golm.Provider = (*Client)(nil)

// New returns a Client for the given configuration.
func New(cfg Config) *Client {
	if cfg.Via == "" {
		cfg.Via = PromptViaStdin
	}
	if cfg.Placeholder == "" {
		cfg.Placeholder = "{{prompt}}"
	}
	if cfg.Name == "" {
		cfg.Name = "cli:" + cfg.Command
	}
	return &Client{cfg: cfg}
}

// Name reports the provider name.
func (c *Client) Name() string { return c.cfg.Name }

// Capabilities reports supported features.
func (c *Client) Capabilities() golm.Capabilities {
	return golm.Capabilities{Streaming: true}
}

func renderPrompt(req golm.Request) string {
	var b strings.Builder
	if sys := req.System.Text(); sys != "" {
		b.WriteString("[system] ")
		b.WriteString(sys)
		b.WriteString("\n\n")
	}
	for _, m := range req.Messages {
		text := messageText(m)
		if text == "" {
			continue
		}
		fmt.Fprintf(&b, "[%s] %s\n\n", m.Role, text)
	}
	return strings.TrimSpace(b.String())
}

func messageText(m golm.Message) string {
	var b strings.Builder
	for _, c := range m.Content {
		switch v := c.(type) {
		case golm.Text:
			b.WriteString(v.Text)
		case golm.Plan:
			b.WriteString(strings.Join(v.Steps, "\n"))
		case golm.ToolResult:
			for _, p := range v.Content {
				switch q := p.(type) {
				case golm.Text:
					b.WriteString(q.Text)
				case golm.Image:
					if q.URL != "" {
						fmt.Fprintf(&b, "[image %s %s]", q.MediaType, q.URL)
						continue
					}
					fmt.Fprintf(&b, "[image %s %d bytes]", q.MediaType, len(q.Data))
				case golm.Audio:
					fmt.Fprintf(&b, "[audio %s %d bytes]", q.MediaType, len(q.Data))
				}
			}
		}
	}
	return b.String()
}

func completeUTF8Prefix(b []byte) int {
	i := 0
	for i < len(b) {
		if !utf8.FullRune(b[i:]) {
			break
		}
		_, size := utf8.DecodeRune(b[i:])
		i += size
	}
	return i
}

func (c *Client) command(ctx context.Context, prompt string) (*exec.Cmd, bool) {
	args := make([]string, len(c.cfg.Args))
	usedArg := false
	for i, a := range c.cfg.Args {
		if c.cfg.Via == PromptViaArg && strings.Contains(a, c.cfg.Placeholder) {
			a = strings.ReplaceAll(a, c.cfg.Placeholder, prompt)
			usedArg = true
		}
		args[i] = a
	}
	cmd := exec.CommandContext(ctx, c.cfg.Command, args...)
	stdin := c.cfg.Via == PromptViaStdin || (c.cfg.Via == PromptViaArg && !usedArg)
	if c.cfg.Via == PromptViaArg && !usedArg {
		argPlaceholderMissWarned.Do(func() {
			slog.Warn("clibackend: PromptViaArg set but placeholder not found in any arg — delivering prompt via stdin",
				"placeholder", c.cfg.Placeholder)
		})
	}
	if stdin {
		cmd.Stdin = strings.NewReader(prompt)
	}
	return cmd, stdin
}

// Complete runs the CLI once and returns its stdout as the assistant message.
func (c *Client) Complete(ctx context.Context, req golm.Request) (golm.Response, error) {
	cmd, _ := c.command(ctx, renderPrompt(req))
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return golm.Response{}, ctx.Err()
		}
		return golm.Response{}, fmt.Errorf("clibackend %s: %v: %s", c.cfg.Command, err, strings.TrimSpace(errb.String()))
	}
	return golm.Response{
		Message:    golm.AssistantText(strings.TrimSpace(out.String())),
		StopReason: golm.StopEndTurn,
	}, nil
}

// Stream runs the CLI and forwards stdout to fn as text deltas.
func (c *Client) Stream(ctx context.Context, req golm.Request, fn func(golm.StreamEvent) error) (golm.Response, error) {
	cmd, _ := c.command(ctx, renderPrompt(req))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return golm.Response{}, err
	}
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		return golm.Response{}, err
	}
	var full strings.Builder
	reader := bufio.NewReader(stdout)
	buf := make([]byte, 4096)
	var pending []byte
	emit := func(s string) error {
		full.WriteString(s)
		if ferr := fn(golm.StreamEvent{Type: golm.EventTextDelta, Text: s}); ferr != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return ferr
		}
		return nil
	}
	for {
		n, rerr := reader.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)

			if k := completeUTF8Prefix(pending); k > 0 {
				if err := emit(string(pending[:k])); err != nil {
					return golm.Response{}, err
				}
				pending = append(pending[:0], pending[k:]...)
			}
		}
		if rerr != nil {
			if rerr != io.EOF {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				if ctx.Err() != nil {
					return golm.Response{}, ctx.Err()
				}
				return golm.Response{}, fmt.Errorf("clibackend %s: read output: %v: %s", c.cfg.Command, rerr, strings.TrimSpace(errb.String()))
			}
			break
		}
	}
	if len(pending) > 0 {
		if err := emit(string(pending)); err != nil {
			return golm.Response{}, err
		}
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return golm.Response{}, ctx.Err()
		}
		return golm.Response{}, fmt.Errorf("clibackend %s: %v: %s", c.cfg.Command, err, strings.TrimSpace(errb.String()))
	}
	if err := fn(golm.StreamEvent{Type: golm.EventDone}); err != nil {
		return golm.Response{}, err
	}
	return golm.Response{
		Message:    golm.AssistantText(strings.TrimSpace(full.String())),
		StopReason: golm.StopEndTurn,
	}, nil
}
