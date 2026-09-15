// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"fmt"
	"sync/atomic"
)

// Budget is a token ceiling shared by every agent that holds it.
type Budget struct {
	limit int64
	spent atomic.Int64

	calls atomic.Int64
	lost  atomic.Int64
}

// NewBudget returns a ceiling of limit tokens.
func NewBudget(limit int) *Budget {
	return &Budget{limit: int64(limit)}
}

// Limit is the ceiling, or 0 when unbounded.
func (b *Budget) Limit() int {
	if b == nil {
		return 0
	}
	return int(b.limit)
}

// Spend books one completed provider call.
func (b *Budget) Spend(u Usage) {
	if b == nil {
		return
	}
	b.spent.Add(int64(u.Total()))
	b.calls.Add(1)
	if u.LostCompletions > 0 {
		b.lost.Add(int64(u.LostCompletions))
	}
}

// Spent is the tokens booked so far.
func (b *Budget) Spent() int {
	if b == nil {
		return 0
	}
	return int(b.spent.Load())
}

// Calls is how many provider calls have been booked.
func (b *Budget) Calls() int {
	if b == nil {
		return 0
	}
	return int(b.calls.Load())
}

// LostCompletions is how many billed responses never arrived.
func (b *Budget) LostCompletions() int {
	if b == nil {
		return 0
	}
	return int(b.lost.Load())
}

// Remaining is what is left, or -1 when unbounded.
func (b *Budget) Remaining() int {
	if b == nil || b.limit <= 0 {
		return -1
	}
	if left := b.limit - b.spent.Load(); left > 0 {
		return int(left)
	}
	return 0
}

// Exhausted reports whether the ceiling has been reached.
func (b *Budget) Exhausted() bool {
	if b == nil || b.limit <= 0 {
		return false
	}
	return b.spent.Load() >= b.limit
}

func (b *Budget) err() error {
	return fmt.Errorf("%w: shared budget (%d/%d)", ErrTokenBudget, b.Spent(), b.Limit())
}

// String renders the budget on one line, for a status display.
func (b *Budget) String() string {
	if b == nil {
		return "no budget"
	}
	if b.limit <= 0 {
		return fmt.Sprintf("%d tokens over %d call(s), no ceiling", b.Spent(), b.Calls())
	}
	s := fmt.Sprintf("%d/%d tokens over %d call(s)", b.Spent(), b.Limit(), b.Calls())
	if l := b.LostCompletions(); l > 0 {
		s += fmt.Sprintf(" (+%d billed response(s) lost, tokens unknown)", l)
	}
	return s
}
