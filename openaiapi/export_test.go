// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package openaiapi

// LockCount is how many session locks are held, for the test that proves they do not accumulate.
func LockCount(s *Server) int {
	s.turns.mu.Lock()
	defer s.turns.mu.Unlock()
	return len(s.turns.locks)
}
