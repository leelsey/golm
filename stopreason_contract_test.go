// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/provider/anthropic"
	"github.com/leelsey/golm/provider/google"
	"github.com/leelsey/golm/provider/openai"
)

type stopContract struct {
	pkg string

	body func(vendorReason string) string

	call func(t *testing.T, baseURL string) (golm.Response, error)

	cases map[string]golm.StopReason

	streamFrames func(vendorReason string) string

	newProv func(baseURL string) golm.Provider
}

func stopContracts() []stopContract {
	return []stopContract{
		{
			pkg: "anthropic",
			body: func(r string) string {
				return `{"id":"m","content":[{"type":"text","text":""}],"stop_reason":"` + r + `",` +
					`"usage":{"input_tokens":10,"output_tokens":1}}`
			},
			call: func(t *testing.T, u string) (golm.Response, error) {
				return anthropic.New("k").WithBaseURL(u).WithoutRetry().
					Complete(context.Background(), golm.Request{Model: "claude-opus-5", MaxTokens: 16})
			},
			cases: map[string]golm.StopReason{
				"end_turn":                      golm.StopEndTurn,
				"tool_use":                      golm.StopToolUse,
				"max_tokens":                    golm.StopMaxTokens,
				"refusal":                       golm.StopRefusal,
				"model_context_window_exceeded": golm.StopContextOverflow,
				"pause_turn":                    golm.StopPause,
				"something_new":                 golm.StopOther,
			},
			streamFrames: func(r string) string {
				return sse("event: message_start",
					`data: {"type":"message_start","message":{"id":"m","model":"x","usage":{"input_tokens":10,"output_tokens":1}}}`) +
					sse("event: message_delta",
						`data: {"type":"message_delta","delta":{"stop_reason":"`+r+`"},"usage":{"output_tokens":2}}`)
			},
			newProv: func(u string) golm.Provider { return anthropic.New("k").WithBaseURL(u) },
		},
		{
			pkg: "openai",
			body: func(r string) string {
				return `{"id":"c","choices":[{"index":0,"message":{"role":"assistant","content":""},` +
					`"finish_reason":"` + r + `"}],"usage":{"prompt_tokens":10,"completion_tokens":1}}`
			},
			call: func(t *testing.T, u string) (golm.Response, error) {
				return openai.New("k").WithBaseURL(u).WithoutRetry().
					Complete(context.Background(), golm.Request{Model: "gpt-4o", MaxTokens: 16})
			},
			cases: map[string]golm.StopReason{
				"stop":           golm.StopEndTurn,
				"tool_calls":     golm.StopToolUse,
				"length":         golm.StopMaxTokens,
				"content_filter": golm.StopRefusal,
				"something_new":  golm.StopOther,
			},
			streamFrames: func(r string) string {
				return sse(`data: {"id":"1","model":"x","choices":[{"index":0,"delta":{"content":"hi"}}],"usage":{"prompt_tokens":10,"completion_tokens":1}}`) +
					sse(`data: {"id":"1","model":"x","choices":[{"index":0,"delta":{},"finish_reason":"`+r+`"}],"usage":{"completion_tokens":2}}`) +
					sse("data: [DONE]")
			},
			newProv: func(u string) golm.Provider { return openai.New("k").WithBaseURL(u) },
		},
		{
			pkg: "google",
			body: func(r string) string {
				return `{"candidates":[{"content":{"role":"model","parts":[{"text":""}]},"finishReason":"` + r + `"}],` +
					`"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":1}}`
			},
			call: func(t *testing.T, u string) (golm.Response, error) {
				return google.New("k").WithBaseURL(u).WithoutRetry().
					Complete(context.Background(), golm.Request{Model: "gemini-2.0-flash", MaxTokens: 16})
			},
			cases: map[string]golm.StopReason{
				"STOP":               golm.StopEndTurn,
				"MAX_TOKENS":         golm.StopMaxTokens,
				"SAFETY":             golm.StopRefusal,
				"RECITATION":         golm.StopRefusal,
				"PROHIBITED_CONTENT": golm.StopRefusal,
				"SOMETHING_NEW":      golm.StopOther,
			},
			streamFrames: func(r string) string {
				return sse(`data: {"candidates":[{"content":{"parts":[{"text":"hi"}]}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":1}}`) +
					sse(`data: {"candidates":[{"content":{"parts":[{"text":""}]},"finishReason":"`+r+`"}],"usageMetadata":{"candidatesTokenCount":2}}`)
			},
			newProv: func(u string) golm.Provider { return google.New("k").WithBaseURL(u) },
		},
	}
}

func TestStopReasonSurvivesTheAdapter(t *testing.T) {
	for _, c := range stopContracts() {
		for vendor, want := range c.cases {
			t.Run(c.pkg+"/"+vendor, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("content-type", "application/json")
					_, _ = w.Write([]byte(c.body(vendor)))
				}))
				defer srv.Close()

				resp, err := c.call(t, srv.URL)
				if err != nil {
					t.Fatalf("Complete: %v", err)
				}
				if resp.StopReason != want {
					t.Errorf("%q became %q, want %q — a caller cannot act on a reason it never sees",
						vendor, resp.StopReason, want)
				}
			})
		}
	}
}

// A refusal carries no text.
func TestARefusalIsNotAnEmptyAnswer(t *testing.T) {
	for _, c := range stopContracts() {
		var vendorRefusal string
		for vendor, want := range c.cases {
			if want == golm.StopRefusal {
				vendorRefusal = vendor
				break
			}
		}
		if vendorRefusal == "" {
			t.Errorf("%s: no vendor spelling of a refusal is under contract", c.pkg)
			continue
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("content-type", "application/json")
			_, _ = w.Write([]byte(c.body(vendorRefusal)))
		}))
		resp, err := c.call(t, srv.URL)
		srv.Close()
		if err != nil {
			t.Fatalf("%s: Complete: %v", c.pkg, err)
		}
		if strings.TrimSpace(resp.Message.Text()) != "" {
			t.Fatalf("%s: fixture is wrong — a refusal with text proves nothing", c.pkg)
		}
		if resp.StopReason == golm.StopEndTurn || resp.StopReason == golm.StopOther {
			t.Errorf("%s: an empty refusal reports %q, indistinguishable from a model that simply finished",
				c.pkg, resp.StopReason)
		}
	}
}

// The agent loop streams by default.
func TestStopReasonSurvivesTheStream(t *testing.T) {
	for _, c := range stopContracts() {
		if c.streamFrames == nil {
			t.Errorf("%s: no streaming case — the default transport is untested", c.pkg)
			continue
		}
		for vendor, want := range c.cases {
			t.Run(c.pkg+"/"+vendor, func(t *testing.T) {
				body := c.streamFrames(vendor)
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("content-type", "text/event-stream")
					_, _ = w.Write([]byte(body))
				}))
				defer srv.Close()

				resp, err := c.newProv(srv.URL).Stream(context.Background(),
					golm.Request{Model: "x", Messages: []golm.Message{golm.UserText("hi")}, MaxTokens: 64},
					func(golm.StreamEvent) error { return nil })
				if err != nil {
					t.Fatalf("stream: %v", err)
				}
				if resp.StopReason != want {
					t.Errorf("%q became %q over the stream, want %q", vendor, resp.StopReason, want)
				}
			})
		}
	}
}

func TestEveryProviderIsUnderTheStopReasonContract(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range stopContracts() {
		covered[c.pkg] = true
	}

	entries, err := os.ReadDir("provider")
	if err != nil {
		t.Fatalf("read provider dir: %v", err)
	}
	var sawAny bool
	for _, e := range entries {
		if !e.IsDir() || !mapsAStopReason(t, filepath.Join("provider", e.Name())) {
			continue
		}
		if why, ok := providerWrappers[e.Name()]; ok {
			t.Logf("provider %q is a wrapper, not an adapter: %s", e.Name(), why)
			continue
		}
		sawAny = true
		if !covered[e.Name()] {
			t.Errorf("provider %q translates a vendor stop reason but is under no contract — add it to "+
				"stopContracts() with the vendor's own spelling of a refusal", e.Name())
		}
	}
	if !sawAny {
		t.Fatal("found no provider mapping a stop reason — the scan is broken, not the tree")
	}
}

func mapsAStopReason(t *testing.T, dir string) bool {
	t.Helper()
	fset := token.NewFileSet()
	//lint:ignore SA1019 the alternative is a third-party dependency this module refuses
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		return false
	}
	found := false
	for _, p := range pkgs {
		for _, f := range p.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "StopOther" {
					return true
				}
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "golm" {
					found = true
				}
				return true
			})
		}
	}
	return found
}
