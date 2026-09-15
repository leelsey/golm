// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package netbus

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/internal/sse"
)

// Client connects a process to a netbus Hub.
type Client struct {
	hubURL      string
	origin      string
	token       string
	http        *http.Client
	pubFailures atomic.Int64
}

// PublishFailures reports how many outbound Bridge publishes have failed since the client was created.
func (c *Client) PublishFailures() int64 { return c.pubFailures.Load() }

// NewClient returns a Client for the hub at hubURL.
func NewClient(hubURL string) *Client {
	return &Client{hubURL: strings.TrimRight(hubURL, "/"), origin: newOrigin(), http: &http.Client{}}
}

// WithToken sets the bearer token sent on every hub request, for hubs serving with an AuthToken.
func (c *Client) WithToken(token string) *Client { c.token = token; return c }

func (c *Client) authorise(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

// Origin returns this client's unique origin id.
func (c *Client) Origin() string { return c.origin }

func newOrigin() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "origin"
	}
	return hex.EncodeToString(b[:])
}

// Publish sends one event to the hub, tagged with this client's origin.
func (c *Client) Publish(ctx context.Context, ev Event) error {
	ev.Origin = c.origin
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.hubURL+"/publish", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	c.authorise(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("netbus: publish returned status %d", resp.StatusCode)
	}
	return nil
}

// Bridge connects a local golm.Bus to the hub bidirectionally until ctx is done.
func (c *Client) Bridge(ctx context.Context, bus *golm.Bus, topics ...string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go c.receive(ctx, bus, topics)
	allow := make(map[string]bool, len(topics))
	for _, t := range topics {
		allow[t] = true
	}
	events, unsub := bus.Subscribe("*")
	defer unsub()
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			if ev.Origin != "" {
				continue
			}
			if len(allow) > 0 && !allow[ev.Topic] {
				continue
			}
			if err := c.Publish(ctx, Event{Topic: ev.Topic, Agent: ev.Agent, Kind: ev.Kind, Data: marshalData(ev.Data)}); err != nil {
				c.pubFailures.Add(1)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *Client) receive(ctx context.Context, bus *golm.Bus, topics []string) {
	u := c.hubURL + "/subscribe"
	if len(topics) > 0 {
		u += "?topics=" + url.QueryEscape(strings.Join(topics, ","))
	}
	const (
		initialBackoff = 100 * time.Millisecond
		maxBackoff     = 30 * time.Second
		minUptime      = 5 * time.Second
	)
	var lastID int64
	backoff := initialBackoff
	for ctx.Err() == nil {
		start := time.Now()
		lastID = c.stream(ctx, u, lastID, bus)

		stable := time.Since(start) >= minUptime
		if stable {
			backoff = initialBackoff
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		if !stable {
			if backoff *= 2; backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

func (c *Client) stream(ctx context.Context, u string, lastID int64, bus *golm.Bus) int64 {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return lastID
	}
	req.Header.Set("accept", "text/event-stream")
	c.authorise(req)
	if lastID > 0 {
		req.Header.Set("Last-Event-ID", strconv.FormatInt(lastID, 10))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return lastID
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return lastID
	}
	scanner := sse.NewScanner(resp.Body)
	for {
		ev, serr := scanner.Next()
		if serr != nil {
			if !errors.Is(serr, io.EOF) {
				slog.Warn("netbus: stream ended with error", "last_id", lastID, "err", serr)
			}
			return lastID
		}
		if ev.Data == "" {
			continue
		}
		var we Event
		if json.Unmarshal([]byte(ev.Data), &we) != nil {
			continue
		}
		if we.ID != 0 && we.ID <= lastID {
			continue
		}
		if we.ID > lastID {
			lastID = we.ID
		}
		if we.Origin == c.origin {
			continue
		}
		bus.Publish(ctx, golm.Event{Topic: we.Topic, Agent: we.Agent, Kind: we.Kind, Data: we.Data, Origin: we.Origin})
	}
}

func marshalData(d any) json.RawMessage {
	if d == nil {
		return nil
	}
	b, err := json.Marshal(d)
	if err != nil {
		return nil
	}
	return b
}
