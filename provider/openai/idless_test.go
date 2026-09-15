// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leelsey/golm"
)

func TestStreamIDLessToolCall(t *testing.T) {
	chunks := []string{
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"f","arguments":"{\"x\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			fl.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	defer srv.Close()

	c := New("k").WithBaseURL(srv.URL)
	starts := 0
	var startID, stopID string
	resp, err := c.Stream(context.Background(), golm.Request{Model: "m", Messages: []golm.Message{golm.UserText("hi")}}, func(ev golm.StreamEvent) error {
		switch ev.Type {
		case golm.EventToolStart:
			starts++
			startID = ev.ToolID
		case golm.EventToolStop:
			stopID = ev.ToolID
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if starts != 1 {
		t.Errorf("EventToolStart fired %d times, want 1", starts)
	}
	if startID == "" || startID != stopID {
		t.Errorf("tool_start id %q != tool_stop id %q; consumers pairing by ToolID mispair", startID, stopID)
	}
	uses := resp.Message.ToolUses()
	if len(uses) != 1 {
		t.Fatalf("ToolUses = %d, want 1 (id-less call swallowed)", len(uses))
	}
	if uses[0].ID == "" || uses[0].Name != "f" || string(uses[0].Input) != `{"x":1}` {
		t.Errorf("ToolUse = %+v, want synthesised id, name f, assembled args", uses[0])
	}
}
