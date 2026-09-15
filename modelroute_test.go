// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import "testing"

func TestProviderTypeForModel(t *testing.T) {
	cases := []struct {
		model string
		want  string
		ok    bool
	}{
		{"claude-opus-5", "anthropic", true},
		{"claude-sonnet-5", "anthropic", true},
		{"anthropic/claude-opus-5", "anthropic", true},
		{"anthropic.claude-3-7-sonnet-20250219-v1:0", "anthropic", true},
		{"gpt-5", "openai", true},
		{"GPT-5", "openai", true},
		{"o3-mini", "openai", true},
		{"chatgpt-4o-latest", "openai", true},
		{"openai/gpt-5", "openai", true},
		{"gemini-3-pro", "google", true},
		{"models/gemini-2.5-flash", "google", true},
		{"google/gemini-3-pro", "google", true},
		{"Hermes-4-405B", "openai", true},
		{"qwen3-coder", "openai", true},
		{"deepseek-r1", "openai", true},
		{"llama-4-scout", "openai", true},
		{"", "", false},
		{"some-local-finetune", "", false},
		{"acme/whatever", "", false},
	}
	for _, c := range cases {
		got, ok := ProviderTypeForModel(c.model)
		if got != c.want || ok != c.ok {
			t.Errorf("ProviderTypeForModel(%q) = %q,%v; want %q,%v", c.model, got, ok, c.want, c.ok)
		}
	}
}

// An unrecognised name must not be guessed into a provider.
func TestProviderTypeForModelRefusesToGuess(t *testing.T) {
	if _, ok := ProviderTypeForModel("totally-made-up-v9"); ok {
		t.Error("an unknown model resolved to a provider")
	}
}

func TestDefaultKeyEnvForProvider(t *testing.T) {
	for prov, want := range map[string]string{
		"anthropic": "ANTHROPIC_API_KEY",
		"openai":    "OPENAI_API_KEY",
		"google":    "GEMINI_API_KEY",
		"cli":       "",
		"":          "",
	} {
		if got := DefaultKeyEnvForProvider(prov); got != want {
			t.Errorf("DefaultKeyEnvForProvider(%q) = %q, want %q", prov, got, want)
		}
	}
}
