// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"sync"
)

type streamSinkKey struct{}

type sink struct {
	mu sync.Mutex
	fn func(StreamEvent) error

	depth int
}

func (s *sink) emit(ev StreamEvent) error {
	if s == nil || s.fn == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fn(ev)
}

func withStreamSink(ctx context.Context, s *sink) context.Context {
	return context.WithValue(ctx, streamSinkKey{}, s)
}

func streamSinkOf(ctx context.Context) *sink {
	s, _ := ctx.Value(streamSinkKey{}).(*sink)
	return s
}

func withoutStreamSink(ctx context.Context) context.Context {
	if streamSinkOf(ctx) == nil {
		return ctx
	}
	return context.WithValue(ctx, streamSinkKey{}, (*sink)(nil))
}
