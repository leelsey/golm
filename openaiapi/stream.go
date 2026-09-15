// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package openaiapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/leelsey/golm"
)

func (s *Server) streamChat(ctx context.Context, w http.ResponseWriter, id string, req chatRequest, entry *served, sess *golm.Session, turn golm.Message) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.completeChat(ctx, w, id, req, entry, sess, turn)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")

	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	created := time.Now().Unix()
	send := func(v any) bool {
		b, err := json.Marshal(v)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	chunk := func(delta *outMessage, reason *string, usage *chatUsage) chatResponse {
		c := chatResponse{
			ID: id, Object: "chat.completion.chunk", Created: created, Model: req.Model,
			Choices: []chatChoice{{Index: 0, Delta: delta, FinishReason: reason}},
		}
		if usage != nil {
			c.Choices = []chatChoice{}
			c.Usage = usage
		}
		return c
	}
	done := func() {
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}

	if !send(chunk(&outMessage{Role: "assistant"}, nil, nil)) {
		return
	}

	var broken bool
	res, runErr := entry.runner.StreamMessage(ctx, sess, turn, func(ev golm.StreamEvent) error {
		if broken {
			return nil
		}
		switch ev.Type {
		case golm.EventTextDelta:

			if ev.Depth > 0 || ev.Text == "" {
				return nil
			}
			if !send(chunk(&outMessage{Content: ev.Text}, nil, nil)) {
				broken = true
			}
		}
		return nil
	})
	s.save(ctx, req.Session, entry, sess)
	if broken || ctx.Err() != nil {
		return
	}

	if runErr != nil {
		send(errorChunk(id, req.Model, created, runErr, res))
		done()
		return
	}
	reason := finishReason(res.StopReason)
	if !send(chunk(&outMessage{}, &reason, nil)) {
		return
	}
	if req.StreamOptions != nil && req.StreamOptions.IncludeUsage {
		if !send(chunk(nil, nil, usageOf(res.Usage))) {
			return
		}
	}
	done()
}

type streamError struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
	Error   apiErrorBody `json:"error"`
}

func errorChunk(id, model string, created int64, err error, res golm.Result) streamError {
	reason := "stop"
	body := apiErrorBody{Message: err.Error(), Type: "server_error", Code: "upstream_error"}
	switch {
	case errors.Is(err, golm.ErrRefused):
		reason = "content_filter"
		body = apiErrorBody{Message: "the model declined to answer",
			Type: "invalid_request_error", Code: "content_filter"}
	case errors.Is(err, golm.ErrContextOverflow):
		reason = "length"
		body = apiErrorBody{Message: "the conversation is longer than the model's context window",
			Type: "invalid_request_error", Code: "context_length_exceeded"}
	case errors.Is(err, golm.ErrTokenBudget):
		body = apiErrorBody{Message: err.Error(),
			Type: "insufficient_quota", Code: "token_budget_exhausted"}
	case errors.Is(err, golm.ErrMaxSteps):
		body = apiErrorBody{
			Message: fmt.Sprintf("the agent did not reach an answer within its step limit (%d steps)", res.Steps),
			Type:    "server_error", Code: "max_steps"}
	}
	return streamError{
		ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
		Choices: []chatChoice{{Index: 0, Delta: &outMessage{}, FinishReason: &reason}},
		Error:   body,
	}
}
