// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

// Package google implements golm.Provider against the Google Gemini generateContent API using only the standard library.
package google

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/httpretry"
	"github.com/leelsey/golm/provider/internal/httpwire"
)

const defaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// Client is a Google Gemini provider.
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
func (c *Client) Name() string { return "google" }

// Capabilities reports supported features.
func (c *Client) Capabilities() golm.Capabilities {
	return golm.Capabilities{
		Streaming: true, Tools: true, Thinking: true, Images: true, Audio: true,
		Safety: true, ResponseModalities: true,
	}
}

func (c *Client) post(ctx context.Context, model, method, query string, body apiRequest) (*http.Response, httpretry.Stats, error) {
	url := fmt.Sprintf("%s/models/%s:%s", c.baseURL, model, method)
	if query != "" {
		url += "?" + query
	}
	return httpwire.Post(ctx, c.http, c.retry, httpwire.Request{
		URL:    url,
		APIKey: c.apiKey,
		Header: func(h http.Header) { h.Set("x-goog-api-key", c.apiKey) },
		Body:   body,
	}, func(status int, body string, _ http.Header) error {
		return &golm.ProviderError{Provider: "google", Status: status, Body: body}
	})
}
