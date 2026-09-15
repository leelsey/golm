// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

// Package rpc provides JSON-RPC 2.0 message transport plus a small Server.
package rpc

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// Transport carries framed JSON-RPC messages.
type Transport interface {
	ReadMessage() ([]byte, error)
	WriteMessage([]byte) error
	Close() error
}

// DefaultMaxMessageBytes bounds one framed message.
const DefaultMaxMessageBytes = 32 << 20

const largeFrameReserve = 1 << 20

// ErrMessageTooLarge is a frame past the transport's cap.
var ErrMessageTooLarge = errors.New("rpc: message exceeds the maximum frame size")

type streamTransport struct {
	r   *bufio.Reader
	w   io.Writer
	wmu sync.Mutex
	c   io.Closer
	max int
}

// NewStreamTransport frames newline-delimited JSON over r/w.
func NewStreamTransport(r io.Reader, w io.Writer, c io.Closer) Transport {
	return &streamTransport{r: bufio.NewReader(r), w: w, c: c, max: DefaultMaxMessageBytes}
}

// NewStreamTransportWithLimit is NewStreamTransport with an explicit frame cap.
func NewStreamTransportWithLimit(r io.Reader, w io.Writer, c io.Closer, max int) Transport {
	if max <= 0 {
		max = DefaultMaxMessageBytes
	}
	return &streamTransport{r: bufio.NewReader(r), w: w, c: c, max: max}
}

// Stdio returns a Transport over the process stdin/stdout.
func Stdio() Transport { return NewStreamTransport(os.Stdin, os.Stdout, nil) }

func (t *streamTransport) ReadMessage() ([]byte, error) {
	for {
		line, err := t.readLine()
		trimmed := bytes.TrimRight(line, "\r\n")
		if len(trimmed) > 0 {
			return trimmed, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func (t *streamTransport) limit() int {
	if t.max <= 0 {
		return DefaultMaxMessageBytes
	}
	return t.max
}

func (t *streamTransport) readLine() ([]byte, error) {
	limit := t.limit()
	var buf []byte
	for {
		chunk, err := t.r.ReadSlice('\n')
		if len(buf)+len(chunk) > limit {
			return nil, fmt.Errorf("%w (%d bytes)", ErrMessageTooLarge, limit)
		}

		if cap(buf)-len(buf) < len(chunk) {
			want := max(2*cap(buf), largeFrameReserve)
			buf = append(make([]byte, 0, min(want, limit)), buf...)
		}
		buf = append(buf, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return buf, err
	}
}

func (t *streamTransport) WriteMessage(b []byte) error {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	if _, err := t.w.Write(b); err != nil {
		return err
	}
	_, err := t.w.Write([]byte{'\n'})
	return err
}

func (t *streamTransport) Close() error {
	if t.c != nil {
		return t.c.Close()
	}
	return nil
}

// NewPipe returns two in-process transports wired to each other, for tests and running an MCP server.
func NewPipe() (Transport, Transport) {
	r1, w1 := io.Pipe()
	r2, w2 := io.Pipe()
	end1 := &streamTransport{r: bufio.NewReader(r1), w: w2, c: multiCloser{r1, w2}}
	end2 := &streamTransport{r: bufio.NewReader(r2), w: w1, c: multiCloser{r2, w1}}
	return end1, end2
}

type multiCloser []io.Closer

func (m multiCloser) Close() error {
	var err error
	for _, c := range m {
		if e := c.Close(); e != nil && err == nil {
			err = e
		}
	}
	return err
}
