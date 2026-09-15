// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package clibackend

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

func requireCat(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat not available")
	}
}

func TestCompleteStdin(t *testing.T) {
	requireCat(t)
	c := New(Config{Command: "cat"})
	resp, err := c.Complete(context.Background(), golm.Request{
		System:   golm.SystemPrompt{{Text: "be nice"}},
		Messages: []golm.Message{golm.UserText("hi there")},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	got := resp.Message.Text()
	if !strings.Contains(got, "[system] be nice") || !strings.Contains(got, "[user] hi there") {
		t.Errorf("rendered prompt not echoed: %q", got)
	}
	if resp.StopReason != golm.StopEndTurn {
		t.Errorf("stop = %q", resp.StopReason)
	}
}

func TestStream(t *testing.T) {
	requireCat(t)
	c := New(Config{Command: "cat"})
	var streamed string
	resp, err := c.Stream(context.Background(), golm.Request{Messages: []golm.Message{golm.UserText("stream me")}}, func(ev golm.StreamEvent) error {
		if ev.Type == golm.EventTextDelta {
			streamed += ev.Text
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if !strings.Contains(streamed, "stream me") {
		t.Errorf("streamed = %q", streamed)
	}
	if !strings.Contains(resp.Message.Text(), "stream me") {
		t.Errorf("assembled = %q", resp.Message.Text())
	}
}
