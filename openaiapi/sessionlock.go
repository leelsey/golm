// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package openaiapi

import (
	"context"
	"sync"
)

type sessionLocks struct {
	mu    sync.Mutex
	locks map[string]*sessionLock
}

type sessionLock struct {
	gate chan struct{}
	refs int
}

func (s *sessionLocks) acquire(ctx context.Context, name string) (func(), error) {
	if name == "" {
		return func() {}, nil
	}
	s.mu.Lock()
	if s.locks == nil {
		s.locks = map[string]*sessionLock{}
	}
	l, ok := s.locks[name]
	if !ok {
		l = &sessionLock{gate: make(chan struct{}, 1)}
		s.locks[name] = l
	}
	l.refs++
	s.mu.Unlock()

	release := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		l.refs--
		if l.refs == 0 {
			delete(s.locks, name)
		}
	}

	select {
	case l.gate <- struct{}{}:
		return func() {
			<-l.gate
			release()
		}, nil
	case <-ctx.Done():
		release()
		return nil, ctx.Err()
	}
}
