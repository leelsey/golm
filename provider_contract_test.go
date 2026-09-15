// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/provider/anthropic"
	"github.com/leelsey/golm/provider/google"
	"github.com/leelsey/golm/provider/openai"
)

// guardContract is one adapter's fixtures for the checks whose assertion is the
// same whatever wire format sits underneath. Written out per adapter they drift,
// and the adapter added next is free to omit one.
type guardContract struct {
	pkg     string
	newProv func(baseURL string) golm.Provider

	model     string
	toolUseID string

	// partial is an SSE body carrying content but no terminal event.
	partial string
}

func guardContracts() []guardContract {
	return []guardContract{
		{
			pkg:       "anthropic",
			newProv:   func(u string) golm.Provider { return anthropic.New("k").WithBaseURL(u) },
			model:     "m",
			toolUseID: "t1",
			partial: sse("event: message_start",
				`data: {"type":"message_start","message":{"usage":{"input_tokens":1}}}`) +
				sse("event: content_block_start",
					`data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}`) +
				sse("event: content_block_delta",
					`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`),
		},
		{
			pkg:       "openai",
			newProv:   func(u string) golm.Provider { return openai.New("k").WithBaseURL(u) },
			model:     "gpt-4o",
			toolUseID: "t1",
			partial:   sse(`data: {"choices":[{"delta":{"content":"partial"}}]}`),
		},
		{
			pkg:       "google",
			newProv:   func(u string) golm.Provider { return google.New("k").WithBaseURL(u) },
			model:     "gemini-x",
			toolUseID: "c0",
			partial:   sse(`data: {"candidates":[{"content":{"parts":[{"text":"partial"}]}}]}`),
		},
	}
}

// bothPaths runs check for Complete and Stream against a server that fails the
// test if it is reached: every adapter builds the two paths separately, and a
// guard is easy to reach on one and forget on the other.
func (c guardContract) bothPaths(t *testing.T, req golm.Request, check func(*testing.T, error)) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the adapter sent a request the guard should have refused")
	}))
	defer srv.Close()

	p := c.newProv(srv.URL)
	t.Run("Complete", func(t *testing.T) {
		_, err := p.Complete(context.Background(), req)
		check(t, err)
	})
	t.Run("Stream", func(t *testing.T) {
		_, err := p.Stream(context.Background(), req, func(golm.StreamEvent) error { return nil })
		check(t, err)
	})
}

func TestEveryAdapterReachesTheSharedGuards(t *testing.T) {
	for _, c := range guardContracts() {
		t.Run(c.pkg, func(t *testing.T) {
			t.Run("system messages are text-only", func(t *testing.T) {
				c.bothPaths(t, golm.Request{Model: c.model, Messages: []golm.Message{
					{Role: golm.RoleSystem, Content: []golm.Content{golm.Image{MediaType: "image/png", Data: []byte{1}}}},
					golm.UserText("hi"),
				}}, func(t *testing.T, err error) {
					if err == nil || !strings.Contains(err.Error(), "text-only") {
						t.Fatalf("err = %v, want a text-only system message error", err)
					}
				})
			})

			t.Run("the image tool result claim matches the behaviour", func(t *testing.T) {
				var sent atomic.Bool
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					sent.Store(true)
					w.Header().Set("content-type", "application/json")
					io.WriteString(w, "{}")
				}))
				defer srv.Close()

				p := c.newProv(srv.URL)
				req := golm.Request{Model: c.model, Messages: []golm.Message{{
					Role: golm.RoleTool,
					Content: []golm.Content{golm.ToolResult{
						ToolUseID: c.toolUseID,
						Name:      "screenshot",
						Content: []golm.ToolContent{
							golm.Text{Text: "captured"},
							golm.Image{MediaType: "image/png", Data: []byte("png")},
						},
					}},
				}}}
				_, err := p.Complete(context.Background(), req)

				if p.Capabilities().ToolResultImages {
					if !sent.Load() {
						t.Fatalf("Capabilities claims image tool results, but the request never left: %v", err)
					}
					return
				}
				if sent.Load() {
					t.Fatal("an image tool result this wire format cannot carry was sent anyway")
				}
				if err == nil {
					t.Fatal("an image tool result this wire format cannot carry was accepted")
				}
				if !strings.Contains(err.Error(), "screenshot") || !strings.Contains(err.Error(), "image") {
					t.Errorf("err = %v, want one naming the tool and the kind", err)
				}
			})
		})
	}
}

// A stream that stops early is reported, never returned as a short answer — and
// reported as ErrStreamIncomplete, so a caller can tell it from a refusal.
func TestEveryAdapterReportsATruncatedStream(t *testing.T) {
	for _, c := range guardContracts() {
		t.Run(c.pkg, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("content-type", "text/event-stream")
				io.WriteString(w, c.partial)
			}))
			defer srv.Close()

			_, err := c.newProv(srv.URL).Stream(context.Background(),
				golm.Request{Model: c.model, Messages: []golm.Message{golm.UserText("hi")}},
				func(golm.StreamEvent) error { return nil })
			if !errors.Is(err, golm.ErrStreamIncomplete) {
				t.Fatalf("err = %v, want one wrapping ErrStreamIncomplete", err)
			}
		})
	}
}

var providersWithoutAWire = map[string]string{
	"clibackend": "renders every block as text for an external CLI: it has no wire format " +
		"whose limits it could refuse a request against",
}

// A contract with an opt-out nobody has to take is not a contract.
func TestEveryProviderIsUnderTheGuardContract(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range guardContracts() {
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
		if why, ok := providersWithoutAWire[e.Name()]; ok {
			t.Logf("provider %q is exempt: %s", e.Name(), why)
			continue
		}
		t.Errorf("provider %q implements Stream but is under no guard contract — add it to guardContracts(), "+
			"or to providersWithoutAWire with the reason none of the guards apply", e.Name())
	}

	if !sawAny {
		t.Fatal("found no provider implementing Stream — the scan is broken, not the tree")
	}
	for pkg := range providersWithoutAWire {
		if covered[pkg] {
			t.Errorf("provider %q is both exempt and exercised — delete the exemption", pkg)
		}
	}
}
