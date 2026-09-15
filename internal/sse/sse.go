// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

// Package sse parses Server-Sent Events streams using only the standard library.
package sse

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

const maxEventBytes = 16 << 20

// Event is a single server-sent event.
type Event struct {
	Type  string
	Data  string
	ID    string
	Retry string
}

// Scanner reads Events from an SSE stream.
type Scanner struct {
	r       *bufio.Reader
	bomDone bool
}

// NewScanner returns a Scanner reading from r.
func NewScanner(r io.Reader) *Scanner {
	return &Scanner{r: bufio.NewReader(r)}
}

// Next returns the next event.
func (s *Scanner) Next() (Event, error) {
	var ev Event
	var data []string
	var size int
	dispatch := func() (Event, bool) {
		if len(data) == 0 {
			ev.Type = ""
			return Event{}, false
		}
		ev.Data = strings.Join(data, "\n")
		return ev, true
	}
	for {
		line, err := s.readLine()
		if len(line) > 0 {
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "":
				if e, ok := dispatch(); ok {
					return e, nil
				}
			case strings.HasPrefix(line, ":"):

			default:
				field, value, _ := strings.Cut(line, ":")
				value = strings.TrimPrefix(value, " ")
				switch field {
				case "event":
					ev.Type = value
				case "data":
					size += len(value) + 1
					if size > maxEventBytes {
						return Event{}, fmt.Errorf("sse: event exceeds %d bytes", maxEventBytes)
					}
					data = append(data, value)
				case "id":
					if !strings.ContainsRune(value, '\x00') {
						ev.ID = value
					}
				case "retry":
					ev.Retry = value
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				if e, ok := dispatch(); ok {
					return e, nil
				}
			}
			return Event{}, err
		}
	}
}

func (s *Scanner) stripBOM() {
	if s.bomDone {
		return
	}
	s.bomDone = true
	if b, err := s.r.Peek(3); err == nil && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		_, _ = s.r.Discard(3)
	}
}

func (s *Scanner) readLine() (string, error) {
	s.stripBOM()
	var sb strings.Builder
	for {
		b, err := s.r.ReadByte()
		if err != nil {
			return sb.String(), err
		}
		switch b {
		case '\n':
			sb.WriteByte('\n')
			return sb.String(), nil
		case '\r':
			if nb, err := s.r.ReadByte(); err == nil && nb != '\n' {
				_ = s.r.UnreadByte()
			}
			sb.WriteByte('\n')
			return sb.String(), nil
		default:
			if sb.Len() >= maxEventBytes {
				return "", fmt.Errorf("sse: line exceeds %d bytes", maxEventBytes)
			}
			sb.WriteByte(b)
		}
	}
}
