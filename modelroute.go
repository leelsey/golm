// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import "strings"

// ProviderTypeForModel reports which backend a model name belongs to.
func ProviderTypeForModel(model string) (string, bool) {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return "", false
	}

	if vendor, rest, ok := strings.Cut(m, "/"); ok && rest != "" {
		if t, known := vendorType(vendor); known {
			return t, true
		}
		m = rest
	}
	if vendor, _, ok := strings.Cut(m, "."); ok {
		if t, known := vendorType(vendor); known {
			return t, true
		}
	}
	for _, r := range modelRoutes {
		for _, p := range r.prefixes {
			if strings.HasPrefix(m, p) {
				return r.provider, true
			}
		}
	}
	return "", false
}

func vendorType(v string) (string, bool) {
	switch v {
	case "anthropic":
		return "anthropic", true
	case "openai", "azure":
		return "openai", true
	case "google", "googleai", "gemini", "vertex":
		return "google", true
	}
	return "", false
}

var modelRoutes = []struct {
	provider string
	prefixes []string
}{
	{"anthropic", []string{"claude"}},
	{"google", []string{"gemini", "models/gemini", "gemma", "text-bison", "learnlm"}},
	{"openai", []string{
		"gpt", "chatgpt", "o1", "o3", "o4", "davinci", "babbage",

		"hermes", "nous", "llama", "meta-llama", "qwen", "mistral", "mixtral",
		"deepseek", "phi", "yi-", "command-r", "glm-", "kimi", "minimax",
	}},
}

// DefaultKeyEnvForProvider is the environment variable a provider type reads its key from.
func DefaultKeyEnvForProvider(providerType string) string {
	switch providerType {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	case "google":
		return "GEMINI_API_KEY"
	}
	return ""
}
