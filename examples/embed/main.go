// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

// Example: embedding golm as a library with a custom tool.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/provider/anthropic"
)

func main() {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		fmt.Println("set ANTHROPIC_API_KEY to run this example")
		return
	}

	tools := golm.NewRegistry()
	tools.Register(golm.NewTool("add", "add two integers a and b",
		json.RawMessage(`{"type":"object","properties":{"a":{"type":"integer"},"b":{"type":"integer"}},"required":["a","b"]}`),
		func(_ context.Context, in json.RawMessage) (string, error) {
			var args struct{ A, B int }
			if err := json.Unmarshal(in, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("%d", args.A+args.B), nil
		}))

	agent := &golm.Agent{
		Provider: anthropic.New(key),
		Model:    "claude-sonnet-4-6",
		System:   "You are a helpful assistant. Use tools when needed.",
		Tools:    tools,
	}

	res, err := agent.Run(context.Background(), golm.NewSession(), "What is 21 + 21? Use the add tool.")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(res.Text())
}
