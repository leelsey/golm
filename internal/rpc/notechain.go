// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package rpc

import "context"

type noteChain struct{ prev chan struct{} }

func newNoteChain() *noteChain {
	first := make(chan struct{})
	close(first)
	return &noteChain{prev: first}
}

func (n *noteChain) next() (wait <-chan struct{}, done chan struct{}) {
	w := n.prev
	d := make(chan struct{})
	n.prev = d
	return w, d
}

func hold(ctx context.Context, wait <-chan struct{}) bool {
	select {
	case <-wait:
		return true
	case <-ctx.Done():
		return false
	}
}
