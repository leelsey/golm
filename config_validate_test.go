// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import "testing"

func TestValidateProviderRequiredFields(t *testing.T) {
	c := &Config{Providers: []ProviderConfig{{Name: "x", Type: "cli"}}}
	if err := c.Validate(); err == nil {
		t.Error("cli provider without command should fail validation")
	}
	c = &Config{Providers: []ProviderConfig{{Name: "y", Type: "anthropic"}}}
	if err := c.Validate(); err == nil {
		t.Error("http provider without api_key_env/api_key_cmd/base_url should fail")
	}
	c = &Config{Providers: []ProviderConfig{{Name: "z", Type: "openai", BaseURL: "http://localhost:11434/v1"}}}
	if err := c.Validate(); err != nil {
		t.Errorf("base_url-only provider should be valid: %v", err)
	}
	c = &Config{
		Providers: []ProviderConfig{{Name: "cli", Type: "cli", Command: "cat"}},
		Agents:    []PersonaConfig{{Name: "a", Provider: "cli", Model: "cli", Tools: []string{"calc"}}},
	}
	if err := c.Validate(); err == nil {
		t.Error("cli-backed persona with tools should fail validation")
	}
}
