// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func blockingTool(entered chan<- struct{}, release <-chan struct{}) Tool {
	return NewTool("block", "blocks", json.RawMessage(`{"type":"object"}`),
		func(ctx context.Context, _ json.RawMessage) (string, error) {
			entered <- struct{}{}
			<-release
			return "done", nil
		})
}

func TestConcurrentRunIsRefused(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	reg := NewRegistry()
	reg.Register(blockingTool(entered, release))

	fp := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "c1", Name: "block", Input: json.RawMessage(`{}`)},
		}}, StopReason: StopToolUse},
		{Message: AssistantText("final"), StopReason: StopEndTurn},
	}}
	sess := NewSession()
	a := &Agent{Provider: fp, Model: "x", Tools: reg}

	done := make(chan error, 1)
	go func() {
		_, err := a.Run(context.Background(), sess, "first")
		done <- err
	}()
	<-entered

	if _, err := a.Run(context.Background(), sess, "second"); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("second Run on the same Session err = %v, want ErrSessionBusy", err)
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first Run must still finish cleanly, got %v", err)
	}

	for _, m := range sess.History() {
		if m.Role == RoleUser && m.Text() == "second" {
			t.Fatal("the refused Run appended its input to the session")
		}
	}
}

func TestRunReleasesTheGuardOnError(t *testing.T) {
	fp := &erringProvider{}
	sess := NewSession()
	a := &Agent{Provider: fp, Model: "x"}

	if _, err := a.Run(context.Background(), sess, "one"); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	if _, err := a.Run(context.Background(), sess, "two"); errors.Is(err, ErrSessionBusy) {
		t.Fatal("the guard was not released after the previous run")
	}
}
