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

type streamContract struct {
	pkg string

	frames []string

	usageFrame int
	newProv    func(baseURL string) golm.Provider
	wantIn     int
	wantOut    int
	wantRead   int
}

func sse(lines ...string) string { return strings.Join(lines, "\n") + "\n\n" }

func streamContracts() []streamContract {
	return []streamContract{
		{
			pkg: "anthropic",
			frames: []string{
				sse("event: message_start",
					`data: {"type":"message_start","message":{"id":"m","model":"x","usage":{"input_tokens":300,"cache_read_input_tokens":4000,"cache_creation_input_tokens":0,"output_tokens":1}}}`),
				sse("event: message_delta",
					`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":90}}`),
			},
			usageFrame: 1,
			newProv:    func(u string) golm.Provider { return anthropic.New("k").WithBaseURL(u) },
			wantIn:     4300, wantOut: 90, wantRead: 4000,
		},
		{
			pkg: "openai",
			frames: []string{
				sse(`data: {"id":"1","model":"x","choices":[{"index":0,"delta":{"content":"hi"}}],"usage":{"prompt_tokens":4300,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":4000}}}`),
				sse(`data: {"id":"1","model":"x","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"completion_tokens":90}}`),
				sse("data: [DONE]"),
			},
			usageFrame: 1,
			newProv:    func(u string) golm.Provider { return openai.New("k").WithBaseURL(u) },
			wantIn:     4300, wantOut: 90, wantRead: 4000,
		},
		{
			pkg: "google",
			frames: []string{
				sse(`data: {"candidates":[{"content":{"parts":[{"text":"hi"}]}}],"usageMetadata":{"promptTokenCount":4300,"cachedContentTokenCount":4000,"candidatesTokenCount":1}}`),
				sse(`data: {"candidates":[{"content":{"parts":[{"text":""}]},"finishReason":"STOP"}],"usageMetadata":{"candidatesTokenCount":90}}`),
			},
			usageFrame: 1,
			newProv:    func(u string) golm.Provider { return google.New("k").WithBaseURL(u) },
			wantIn:     4300, wantOut: 90, wantRead: 4000,
		},
	}
}

var providersReportingNoUsage = map[string]string{
	"clibackend": "wraps a text-only CLI: it reports no usage at all, by documented design",
}

func TestStreamingProvidersHonourTheUsageContract(t *testing.T) {
	for _, c := range streamContracts() {
		t.Run(c.pkg, func(t *testing.T) {
			for _, repeat := range []bool{false, true} {
				frames := c.frames
				if repeat {
					frames = append(append(append([]string{}, c.frames[:c.usageFrame+1]...),
						c.frames[c.usageFrame]), c.frames[c.usageFrame+1:]...)
				}
				body := strings.Join(frames, "")

				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("content-type", "text/event-stream")
					_, _ = w.Write([]byte(body))
				}))
				resp, err := c.newProv(srv.URL).Stream(context.Background(),
					golm.Request{Model: "x", Messages: []golm.Message{golm.UserText("hi")}, MaxTokens: 64},
					func(golm.StreamEvent) error { return nil })
				srv.Close()
				if err != nil {
					t.Fatalf("repeat=%v: stream: %v", repeat, err)
				}

				u := resp.Usage
				if u.InputTokens != c.wantIn {
					t.Errorf("repeat=%v: InputTokens = %d, want %d — a later frame erased what an earlier one reported, or the frames were summed",
						repeat, u.InputTokens, c.wantIn)
				}
				if u.OutputTokens != c.wantOut {
					t.Errorf("repeat=%v: OutputTokens = %d, want %d", repeat, u.OutputTokens, c.wantOut)
				}
				if u.CacheReadTokens != c.wantRead {
					t.Errorf("repeat=%v: CacheReadTokens = %d, want %d — the cheapest tokens are the easiest to lose",
						repeat, u.CacheReadTokens, c.wantRead)
				}

				if u.CacheReadTokens+u.CacheWriteTokens > u.InputTokens {
					t.Errorf("repeat=%v: cache %d+%d exceeds input %d — this adapter reports cache OUTSIDE the prompt "+
						"count, which turns every consumer's fresh-input arithmetic negative",
						repeat, u.CacheReadTokens, u.CacheWriteTokens, u.InputTokens)
				}
			}
		})
	}
}

// A contract with an opt-out nobody has to take is not a contract.
func TestEveryProviderIsUnderTheUsageContract(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range streamContracts() {
		covered[c.pkg] = true
	}

	entries, err := os.ReadDir("provider")
	if err != nil {
		t.Fatalf("read provider dir: %v", err)
	}
	var sawAny bool
	for _, e := range entries {
		if !e.IsDir() || !declaresStream(t, filepath.Join("provider", e.Name())) {
			continue
		}
		if why, ok := providerWrappers[e.Name()]; ok {
			t.Logf("provider %q is a wrapper, not an adapter: %s", e.Name(), why)
			continue
		}
		sawAny = true
		if covered[e.Name()] {
			continue
		}
		if why, ok := providersReportingNoUsage[e.Name()]; ok {
			t.Logf("provider %q is exempt: %s", e.Name(), why)
			continue
		}
		t.Errorf("provider %q implements Stream but is under no usage contract — add it to streamContracts(), "+
			"or to providersReportingNoUsage with the reason it reports none", e.Name())
	}

	if !sawAny {
		t.Fatal("found no provider implementing Stream — the scan is broken, not the tree")
	}
	for pkg := range providersReportingNoUsage {
		if covered[pkg] {
			t.Errorf("provider %q is both exempt and exercised — delete the exemption", pkg)
		}
	}
}

func declaresStream(t *testing.T, dir string) bool {
	t.Helper()
	fset := token.NewFileSet()
	//lint:ignore SA1019 the alternative is a third-party dependency this module refuses
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		return false
	}
	for _, p := range pkgs {
		for _, f := range p.Files {
			for _, d := range f.Decls {
				if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv != nil && fn.Name.Name == "Stream" {
					return true
				}
			}
		}
	}
	return false
}
