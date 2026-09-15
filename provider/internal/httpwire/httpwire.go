// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

// Package httpwire carries the HTTP mechanics every provider adapter repeats.
package httpwire

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/httpretry"
)

// maxErrorBody bounds what is read from a failed response.
const maxErrorBody = 64 << 10

// Request is one provider call: where to send it, how to authenticate it, and what to send.
type Request struct {
	URL    string
	APIKey string
	Header func(h http.Header)
	Body   any
}

// Post sends r and returns the response, or the error Fail builds from a non-2xx one.
//
// The failure path is why this is shared: the body must be bounded, the body
// must be closed, and the API key must be scrubbed before the text travels into
// a log or a tool result. Fail receives a body that is already redacted, so an
// adapter cannot forget.
func Post(ctx context.Context, hc *http.Client, retry httpretry.Policy, r Request,
	fail func(status int, body string, h http.Header) error,
) (*http.Response, httpretry.Stats, error) {
	b, err := json.Marshal(r.Body)
	if err != nil {
		return nil, httpretry.Stats{}, err
	}
	resp, st, err := httpretry.DoStats(ctx, hc, retry, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.URL, bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		req.Header.Set("content-type", "application/json")
		if r.Header != nil {
			r.Header(req.Header)
		}
		return req, nil
	})
	if err != nil {
		return nil, st, err
	}
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		hdr := resp.Header.Clone()
		resp.Body.Close()
		return nil, st, fail(resp.StatusCode, golm.Redact(strings.TrimSpace(string(data)), r.APIKey), hdr)
	}
	return resp, st, nil
}
