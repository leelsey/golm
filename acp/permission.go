// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/leelsey/golm"
)

// Approver asks the person in the EDITOR to approve a tool call.
func (a *Agent) Approver() golm.Approver {
	var mu sync.Mutex
	always := map[string]bool{}
	never := map[string]bool{}

	return func(ctx context.Context, req golm.ToolRequest) (bool, error) {
		name := req.Call.Name
		mu.Lock()
		allowed, denied := always[name], never[name]
		mu.Unlock()
		switch {
		case allowed:
			return true, nil
		case denied:
			return false, nil
		}
		sessionID := SessionIDOf(ctx)
		if sessionID == "" {
			return false, errors.New("acp: no session to ask in")
		}
		if a.peer == nil {
			return false, errors.New("acp: not connected")
		}

		var resp RequestPermissionResponse
		err := a.peer.Call(ctx, MethodRequestPermission, RequestPermissionRequest{
			SessionID: sessionID,
			ToolCall: ToolCallRef{
				ToolCallID: req.Call.ID,
				Title:      describe(req),
				Kind:       KindOf(name),
				Status:     StatusPending,
				RawInput:   req.Call.Input,
			},
			Options: permissionOptions(),
		}, &resp)
		if err != nil {
			a.log(ctx, slog.LevelWarn, "acp: could not ask for permission", "tool", name, "error", err)
			return false, err
		}

		if resp.Outcome.Outcome == OutcomeCancelled {
			return false, context.Canceled
		}
		switch resp.Outcome.OptionID {
		case optAllowAlways:
			mu.Lock()
			always[name] = true
			mu.Unlock()
			return true, nil
		case optAllowOnce:
			return true, nil
		case optRejectAlways:
			mu.Lock()
			never[name] = true
			mu.Unlock()
			return false, nil
		case optRejectOnce:
			return false, nil
		default:

			a.log(ctx, slog.LevelWarn, "acp: unknown permission option", "option", resp.Outcome.OptionID)
			return false, nil
		}
	}
}

const (
	optAllowOnce    = "allow-once"
	optAllowAlways  = "allow-always"
	optRejectOnce   = "reject-once"
	optRejectAlways = "reject-always"
)

func permissionOptions() []PermissionOption {
	return []PermissionOption{
		{OptionID: optAllowOnce, Name: "Allow once", Kind: PermAllowOnce},
		{OptionID: optAllowAlways, Name: "Always allow this tool", Kind: PermAllowAlways},
		{OptionID: optRejectOnce, Name: "Reject", Kind: PermRejectOnce},
		{OptionID: optRejectAlways, Name: "Never allow this tool", Kind: PermRejectAlways},
	}
}

func describe(req golm.ToolRequest) string {
	title := req.Call.Name
	if tr, ok := golm.TraitsOf(req.Tool); ok {
		if traits := traitList(tr); traits != "" {
			title += " (" + traits + ")"
		}
	} else {
		title += " (undeclared capabilities)"
	}
	if summary := summarise(req.Call.Input); summary != "" {
		title += ": " + summary
	}
	return title
}

func traitList(tr golm.ToolTraits) string {
	var out []string
	for _, t := range []struct {
		name string
		on   bool
	}{
		{"read-only", tr.ReadOnly}, {"filesystem", tr.Filesystem},
		{"network", tr.Network}, {"process", tr.Process},
	} {
		if t.on {
			out = append(out, t.name)
		}
	}
	if len(out) == 0 {
		return ""
	}
	s := out[0]
	for _, v := range out[1:] {
		s += ", " + v
	}
	return s
}

const maxTitleInput = 120

func summarise(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return truncate(string(raw), maxTitleInput)
	}

	if len(obj) == 1 {
		for _, v := range obj {
			return truncate(fmt.Sprint(v), maxTitleInput)
		}
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return ""
	}
	return truncate(string(b), maxTitleInput)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
