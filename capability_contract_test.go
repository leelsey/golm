// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm_test

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/provider/anthropic"
	"github.com/leelsey/golm/provider/clibackend"
	"github.com/leelsey/golm/provider/google"
	"github.com/leelsey/golm/provider/openai"
)

type capContract struct {
	pkg string

	model string

	newProv func(baseURL string) golm.Provider

	reply string
}

func capContracts() []capContract {
	return []capContract{
		{
			pkg:   "anthropic",
			model: "claude-opus-5",
			newProv: func(u string) golm.Provider {
				return anthropic.New("k").WithBaseURL(u).WithoutRetry()
			},
			reply: `{"id":"m","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{}}`,
		},
		{
			pkg:   "openai",
			model: "gpt-5",
			newProv: func(u string) golm.Provider {
				return openai.New("k").WithBaseURL(u).WithoutRetry()
			},
			reply: `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{}}`,
		},
		{
			pkg:   "google",
			model: "gemini-2.5-pro",
			newProv: func(u string) golm.Provider {
				return google.New("k").WithBaseURL(u).WithoutRetry()
			},
			reply: `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`,
		},
	}
}

type capField struct {
	name string

	bit func(golm.Capabilities) bool

	set func(*golm.Request)

	onWire map[string]func(body map[string]any) bool
}

func capFields() []capField {
	return []capField{
		{
			name: "Effort",
			bit:  func(c golm.Capabilities) bool { return c.Effort },
			set:  func(r *golm.Request) { r.Effort = golm.EffortHigh },
			onWire: map[string]func(map[string]any) bool{
				"anthropic": func(b map[string]any) bool {
					oc, ok := b["output_config"].(map[string]any)
					return ok && oc["effort"] != nil
				},
				"openai": func(b map[string]any) bool { return b["reasoning_effort"] != nil },
				"google": func(map[string]any) bool { return false },
			},
		},
		{
			name: "Safety",
			bit:  func(c golm.Capabilities) bool { return c.Safety },
			set:  func(r *golm.Request) { r.Safety = golm.SafetyNone },
			onWire: map[string]func(map[string]any) bool{
				"anthropic": func(map[string]any) bool { return false },
				"openai":    func(map[string]any) bool { return false },
				"google": func(b map[string]any) bool {
					s, ok := b["safetySettings"].([]any)
					return ok && len(s) > 0
				},
			},
		},
		{
			name: "PromptCaching",
			bit:  func(c golm.Capabilities) bool { return c.PromptCaching },
			set: func(r *golm.Request) {
				r.System = golm.SystemPrompt{}.Add("a stable prefix").Break()
				r.Cache = golm.CacheConfig{MessagePrefix: len(r.Messages)}
			},
			onWire: map[string]func(map[string]any) bool{
				"anthropic": func(b map[string]any) bool {
					blocks, ok := b["system"].([]any)
					if !ok || len(blocks) == 0 {
						return false
					}
					first, ok := blocks[0].(map[string]any)
					return ok && first["cache_control"] != nil
				},
				"openai": func(map[string]any) bool { return false },
				"google": func(map[string]any) bool { return false },
			},
		},
		{
			name: "ResponseModalities",
			bit:  func(c golm.Capabilities) bool { return c.ResponseModalities },
			set:  func(r *golm.Request) { r.ResponseModalities = []string{"audio"} },
			onWire: map[string]func(map[string]any) bool{
				"anthropic": func(map[string]any) bool { return false },
				"openai":    func(b map[string]any) bool { return b["modalities"] != nil },
				"google": func(b map[string]any) bool {
					gc, ok := b["generationConfig"].(map[string]any)
					return ok && gc["responseModalities"] != nil
				},
			},
		},
	}
}

func captureCapRequest(t *testing.T, c capContract, mutate func(*golm.Request)) (map[string]any, error) {
	t.Helper()
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, c.reply)
	}))
	defer srv.Close()

	req := golm.Request{
		Model:     c.model,
		Messages:  []golm.Message{golm.UserText("hi")},
		MaxTokens: 16,
	}
	mutate(&req)
	_, err := c.newProv(srv.URL).Complete(context.Background(), req)
	return body, err
}

func TestCapabilityBitsMatchTheWire(t *testing.T) {
	for _, c := range capContracts() {
		caps := c.newProv("http://unused").Capabilities()
		for _, f := range capFields() {
			t.Run(c.pkg+"/"+f.name, func(t *testing.T) {
				onWire, ok := f.onWire[c.pkg]
				if !ok {
					t.Fatalf("no wire predicate for %s/%s — the table is incomplete", c.pkg, f.name)
				}
				body, err := captureCapRequest(t, c, f.set)
				if err != nil {
					t.Fatalf("Complete: %v", err)
				}
				got := onWire(body)
				switch {
				case f.bit(caps) && !got:
					t.Errorf("Capabilities.%s is true but the field never reached the wire: %v", f.name, body)
				case !f.bit(caps) && got:
					t.Errorf("Capabilities.%s is false yet the field was sent — the claim is wrong, not the code", f.name)
				}
			})
		}
	}
}

// The other half of the promise.
func TestUnsupportedCapabilitiesAreIgnoredNotRejected(t *testing.T) {
	for _, c := range capContracts() {
		caps := c.newProv("http://unused").Capabilities()
		for _, f := range capFields() {
			if f.bit(caps) {
				continue
			}
			t.Run(c.pkg+"/"+f.name, func(t *testing.T) {
				if _, err := captureCapRequest(t, c, f.set); err != nil {
					t.Errorf("setting an unsupported %s must be ignored, got error: %v", f.name, err)
				}
			})
		}
	}
}

// clibackend claims nothing but Streaming.
func TestCLIBackendClaimsOnlyWhatItHas(t *testing.T) {
	caps := clibackend.New(clibackend.Config{Name: "cli", Command: "echo"}).Capabilities()
	if !caps.Streaming {
		t.Error("clibackend streams and must say so")
	}
	for name, claimed := range map[string]bool{
		"Tools": caps.Tools, "Thinking": caps.Thinking, "Images": caps.Images,
		"Audio": caps.Audio, "PromptCaching": caps.PromptCaching, "Effort": caps.Effort,
		"Safety": caps.Safety, "ResponseModalities": caps.ResponseModalities,
		"ToolResultImages": caps.ToolResultImages,
	} {
		if claimed {
			t.Errorf("clibackend claims %s but renders only text", name)
		}
	}
}

func TestEveryProviderIsUnderTheCapabilityContract(t *testing.T) {
	covered := map[string]bool{"clibackend": true}
	for _, c := range capContracts() {
		covered[c.pkg] = true
	}

	entries, err := os.ReadDir("provider")
	if err != nil {
		t.Fatalf("read provider dir: %v", err)
	}
	var sawAny bool
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if why, ok := providerWrappers[e.Name()]; ok {
			t.Logf("provider %q is a wrapper, not an adapter: %s", e.Name(), why)
			continue
		}
		if e.Name() == "internal" {
			if declaresProvider(t, filepath.Join("provider", e.Name())) {
				t.Error("provider/internal implements golm.Provider; an adapter there is under no contract")
			}
			continue
		}
		sawAny = true
		if !covered[e.Name()] {
			t.Errorf("provider %q is under no capability contract — add it to capContracts() with a "+
				"wire predicate for every field, so its Capabilities cannot claim what it does not send", e.Name())
		}
	}
	if !sawAny {
		t.Fatal("found no providers — the scan is broken, not the tree")
	}
}

// declaresProvider reports whether any non-test file under dir asserts golm.Provider.
func declaresProvider(t *testing.T, dir string) bool {
	found := false
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), "golm.Provider = ") {
			found = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return found
}
