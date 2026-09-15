// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

// Package openai implements golm.Provider against the OpenAI Chat Completions API using only the standard library.
package openai

import (
	"context"
	"net/http"
	"strings"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/httpretry"
	"github.com/leelsey/golm/provider/internal/httpwire"
)

const defaultBaseURL = "https://api.openai.com/v1"

// Client is an OpenAI Chat Completions provider.
type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
	retry   httpretry.Policy
}

var _ golm.Provider = (*Client)(nil)

// New returns a Client authenticating with apiKey.
func New(apiKey string) *Client {
	return &Client{apiKey: apiKey, baseURL: defaultBaseURL, http: golm.DefaultHTTPClient(), retry: httpretry.Default()}
}

// WithBaseURL overrides the API base URL.
func (c *Client) WithBaseURL(u string) *Client { c.baseURL = strings.TrimRight(u, "/"); return c }

// WithHTTPClient overrides the underlying *http.Client.
func (c *Client) WithHTTPClient(h *http.Client) *Client { c.http = h; return c }

// WithRetry sets the retry policy.
func (c *Client) WithRetry(p httpretry.Policy) *Client { c.retry = p; return c }

// WithoutRetry disables retries.
func (c *Client) WithoutRetry() *Client { c.retry = httpretry.Policy{}; return c }

// Name reports the provider name.
func (c *Client) Name() string { return "openai" }

// Capabilities reports supported features.
func (c *Client) Capabilities() golm.Capabilities {
	return golm.Capabilities{
		Streaming: true, Tools: true, Images: true, Audio: true, Thinking: true,
		Effort: true, ResponseModalities: true,
	}
}

func (c *Client) post(ctx context.Context, body apiRequest) (*http.Response, httpretry.Stats, error) {
	return httpwire.Post(ctx, c.http, c.retry, httpwire.Request{
		URL:    c.baseURL + "/chat/completions",
		APIKey: c.apiKey,
		Header: func(h http.Header) { h.Set("authorization", "Bearer "+c.apiKey) },
		Body:   body,
	}, func(status int, body string, _ http.Header) error {
		return &golm.ProviderError{Provider: "openai", Status: status, Body: body}
	})
}
