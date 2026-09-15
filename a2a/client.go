// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/internal/jsonrpc"
	"github.com/leelsey/golm/internal/sse"
)

var maxResponseBytes int64 = 32 << 20

// Client calls a remote A2A agent over its JSON-RPC/HTTP binding.
type Client struct {
	base  string
	http  *http.Client
	seq   int64
	token string
}

// NewClient returns a Client for the agent at baseURL.
func NewClient(baseURL string) *Client {
	return &Client{base: strings.TrimRight(baseURL, "/"), http: &http.Client{}}
}

// WithHTTPClient overrides the underlying *http.Client.
func (c *Client) WithHTTPClient(h *http.Client) *Client { c.http = h; return c }

// WithToken sets the bearer token sent on every request, for servers running with an AuthToken.
func (c *Client) WithToken(token string) *Client { c.token = token; return c }

func (c *Client) auth(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

// AgentCard fetches the remote agent's card.
func (c *Client) AgentCard(ctx context.Context) (AgentCard, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+wellKnownPath, nil)
	if err != nil {
		return AgentCard{}, err
	}
	c.auth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return AgentCard{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return AgentCard{}, fmt.Errorf("a2a: agent card status %d", resp.StatusCode)
	}
	var card AgentCard
	return card, json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&card)
}

func (c *Client) call(ctx context.Context, method string, params, result any) error {
	id := atomic.AddInt64(&c.seq, 1)
	p, err := json.Marshal(params)
	if err != nil {
		return err
	}
	body, err := json.Marshal(jsonrpc.Request{
		JSONRPC: jsonrpc.Version, ID: json.RawMessage(strconv.FormatInt(id, 10)), Method: method, Params: p,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	c.auth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var rpcResp jsonrpc.Response
		if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&rpcResp) == nil && rpcResp.Error != nil {
			return rpcResp.Error
		}
		return fmt.Errorf("a2a: %s returned status %d", method, resp.StatusCode)
	}
	var rpcResp jsonrpc.Response
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&rpcResp); err != nil {
		return fmt.Errorf("a2a: decode response: %w", err)
	}
	if rpcResp.Error != nil {
		return rpcResp.Error
	}
	if result != nil && len(rpcResp.Result) > 0 {
		return json.Unmarshal(rpcResp.Result, result)
	}
	return nil
}

func userMessage(text string) Message {
	return Message{Role: "user", Kind: "message", Parts: []Part{TextPart(text)}}
}

func partsText(parts []Part) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

func taskFailure(t *Task) error { return statusFailure(t.Status) }

func statusFailure(st TaskStatus) error {
	switch st.State {
	case StateFailed, StateCanceled, StateRejected:
		msg := ""
		if st.Message != nil {
			msg = st.Message.Text()
		}
		return fmt.Errorf("a2a: task %s: %s", st.State, msg)
	}
	return nil
}

func taskText(t *Task) string {
	var sb strings.Builder
	for _, a := range t.Artifacts {
		for _, p := range a.Parts {
			sb.WriteString(p.Text)
		}
	}
	if sb.Len() == 0 && t.Status.Message != nil {
		sb.WriteString(t.Status.Message.Text())
	}
	return sb.String()
}

// SendMessage sends text and returns the response text.
func (c *Client) SendMessage(ctx context.Context, text string) (string, error) {
	var raw json.RawMessage
	if err := c.call(ctx, "message/send", MessageSendParams{Message: userMessage(text)}, &raw); err != nil {
		return "", err
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("a2a: message/send returned no result")
	}
	var probe struct {
		Kind  string          `json:"kind"`
		Parts json.RawMessage `json:"parts"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return "", fmt.Errorf("a2a: decode message/send result: %w", err)
	}

	if probe.Kind == "message" || (probe.Kind == "" && len(probe.Parts) > 0) {
		var msg Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			return "", fmt.Errorf("a2a: decode message result: %w", err)
		}
		return msg.Text(), nil
	}
	var task Task
	if err := json.Unmarshal(raw, &task); err != nil {
		return "", fmt.Errorf("a2a: decode task result: %w", err)
	}
	if err := taskFailure(&task); err != nil {
		return "", err
	}
	return taskText(&task), nil
}

// SendMessageStream sends text and invokes onDelta for each streamed text chunk.
func (c *Client) SendMessageStream(ctx context.Context, text string, onDelta func(string)) (string, error) {
	id := atomic.AddInt64(&c.seq, 1)
	p, _ := json.Marshal(MessageSendParams{Message: userMessage(text)})
	body, _ := json.Marshal(jsonrpc.Request{
		JSONRPC: jsonrpc.Version, ID: json.RawMessage(strconv.FormatInt(id, 10)), Method: "message/stream", Params: p,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "text/event-stream")
	c.auth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 || !strings.HasPrefix(strings.ToLower(resp.Header.Get("content-type")), "text/event-stream") {
		var rpcResp jsonrpc.Response
		if json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&rpcResp) == nil && rpcResp.Error != nil {
			return "", rpcResp.Error
		}
		if resp.StatusCode >= 400 {
			return "", fmt.Errorf("a2a: stream status %d", resp.StatusCode)
		}
		return "", fmt.Errorf("a2a: unexpected non-SSE response (content-type %q)", resp.Header.Get("content-type"))
	}

	var asm StreamAssembler
	scanner := sse.NewScanner(resp.Body)
	for {
		ev, serr := scanner.Next()
		if serr == io.EOF {
			break
		}
		if serr != nil {
			return "", serr
		}
		if ev.Data == "" {
			continue
		}
		var rpcResp jsonrpc.Response
		if json.Unmarshal([]byte(ev.Data), &rpcResp) != nil {
			continue
		}
		if rpcResp.Error != nil {
			return "", rpcResp.Error
		}
		var kind struct {
			Kind string `json:"kind"`
		}
		_ = json.Unmarshal(rpcResp.Result, &kind)
		switch kind.Kind {
		case "message":

			var msg Message
			if json.Unmarshal(rpcResp.Result, &msg) == nil {
				t := msg.Text()
				asm.Delta(t, true)
				if onDelta != nil {
					onDelta(t)
				}
			}
		case "task":

			var task Task
			if json.Unmarshal(rpcResp.Result, &task) == nil {
				if err := taskFailure(&task); err != nil {
					return "", err
				}
				switch task.Status.State {
				case StateCompleted, StateInputRequired, StateAuthRequired:
					asm.Final(taskText(&task))
				}
			}
		case "status-update":
			var su TaskStatusUpdateEvent
			if json.Unmarshal(rpcResp.Result, &su) == nil {
				if err := statusFailure(su.Status); err != nil {
					return "", err
				}
			}
		case "artifact-update":
			var au TaskArtifactUpdateEvent
			if json.Unmarshal(rpcResp.Result, &au) != nil {
				continue
			}
			text := partsText(au.Artifact.Parts)
			if au.LastChunk {
				asm.Final(text)
				continue
			}
			asm.Delta(text, au.Append)
			if onDelta != nil {
				onDelta(text)
			}
		}
	}
	return asm.Result(), nil
}

// AsTool wraps the remote agent as a golm.Tool that delegates a "task" string.
func (c *Client) AsTool(name, description string) golm.Tool {
	return golm.TextTool(name, description, "task", "the task to delegate to the remote agent",
		func(ctx context.Context, task string) (string, error) {
			return c.SendMessage(ctx, task)
		})
}
