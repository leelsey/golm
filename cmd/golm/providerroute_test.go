// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"
)

// --provider used to default to "anthropic".
func TestAdHocProviderFollowsTheModelName(t *testing.T) {
	cases := map[string]string{
		"claude-opus-5": "anthropic",
		"gpt-5":         "openai",
		"gemini-3-pro":  "google",
		"Hermes-4-70B":  "openai",
	}
	for model, want := range cases {
		cfg, name, err := (&backendFlags{model: model}).resolveConfig("")
		if err != nil {
			t.Fatalf("%s: %v", model, err)
		}
		if len(cfg.Providers) != 1 || cfg.Providers[0].Type != want {
			t.Errorf("%s resolved to %+v, want type %q", model, cfg.Providers, want)
		}
		if p, ok := cfg.Agent(name); !ok || p.Model != model {
			t.Errorf("%s: persona model = %q", model, p.Model)
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("%s: synthesised config is invalid: %v", model, err)
		}
	}
}

func TestExplicitProviderOutranksTheModelName(t *testing.T) {
	cfg, _, err := (&backendFlags{model: "gpt-5", provider: "openai", baseURL: "http://localhost:11434/v1"}).resolveConfig("")
	if err != nil {
		t.Fatalf("resolveConfig: %v", err)
	}
	if cfg.Providers[0].Type != "openai" || cfg.Providers[0].BaseURL == "" {
		t.Errorf("provider = %+v", cfg.Providers[0])
	}
}

func TestUnknownProviderIsRefused(t *testing.T) {
	_, _, err := (&backendFlags{model: "x", provider: "bedrock"}).resolveConfig("")
	if err == nil || !strings.Contains(err.Error(), "--provider") {
		t.Errorf("err = %v, want one naming the flag", err)
	}
}

// A name nothing recognises is an error that says what to do, not a guess.
func TestUnknownModelAsksForTheProvider(t *testing.T) {
	_, _, err := (&backendFlags{model: "my-private-finetune"}).resolveConfig("")
	if err == nil {
		t.Fatal("an unrecognised model silently picked a provider")
	}
	if !strings.Contains(err.Error(), "--provider") {
		t.Errorf("err = %v, should tell the operator which flag to pass", err)
	}
}
