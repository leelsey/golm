// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT
//
// by Leelsey (le@elsey.me.uk) - https://leelsey.me.uk

// Package golm is a lightweight, embeddable LLM agent framework.
package golm

import (
	"context"
	"runtime/debug"
)

const modulePath = "github.com/leelsey/golm"

// Version is the framework version.
var Version = "dev"

func init() {
	if Version != "dev" {
		return
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := moduleVersion(bi); v != "" {
			Version = v
		}
	}
}

func moduleVersion(bi *debug.BuildInfo) string {
	v := ""
	if bi.Main.Path == modulePath {
		v = bi.Main.Version
	} else {
		for _, d := range bi.Deps {
			if d.Path != modulePath {
				continue
			}
			v = d.Version
			if d.Replace != nil {
				v = d.Replace.Version
			}
			break
		}
	}
	if v == "(devel)" {
		return ""
	}
	return v
}

// Runner is something one conversational turn can be run against.
type Runner interface {
	StreamMessage(ctx context.Context, s *Session, msg Message, fn func(StreamEvent) error) (Result, error)
}
