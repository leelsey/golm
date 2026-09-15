// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package anthropic

import (
	"encoding/json"
	"testing"
)

// The write total is one number and the two TTLs behind it are billed 60% apart.
func TestCacheCreationSplitIsCarried(t *testing.T) {
	const body = `{"id":"m","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn",
		"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":2000,
		"cache_creation_input_tokens":1000,
		"cache_creation":{"ephemeral_5m_input_tokens":400,"ephemeral_1h_input_tokens":600}}}`
	var r apiResponse
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	u := r.toResponse().Usage
	if u.CacheWriteTokens != 1000 {
		t.Fatalf("CacheWriteTokens = %d, want 1000", u.CacheWriteTokens)
	}
	if u.CacheWrite5mTokens != 400 || u.CacheWrite1hTokens != 600 {
		t.Errorf("split = 5m %d / 1h %d, want 400/600", u.CacheWrite5mTokens, u.CacheWrite1hTokens)
	}
	if u.InputTokens != 3010 {
		t.Errorf("InputTokens = %d, want 3010 — the split must not change the prompt total", u.InputTokens)
	}
}

// An absent cache_creation object is the ordinary case on older API versions and behind any proxy.
func TestCacheCreationAbsentIsUnknownNotZero(t *testing.T) {
	const body = `{"id":"m","content":[],"usage":{"input_tokens":1,"output_tokens":1,
		"cache_creation_input_tokens":900}}`
	var r apiResponse
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	u := r.toResponse().Usage
	if u.CacheWriteTokens != 900 {
		t.Fatalf("CacheWriteTokens = %d, want 900", u.CacheWriteTokens)
	}
	if u.CacheWrite5mTokens != 0 || u.CacheWrite1hTokens != 0 {
		t.Errorf("split = 5m %d / 1h %d, want 0/0 for an absent breakdown",
			u.CacheWrite5mTokens, u.CacheWrite1hTokens)
	}
}

// A breakdown larger than the total it breaks down has been observed in the wild.
func TestCacheCreationSplitExceedingTotalIsDropped(t *testing.T) {
	const body = `{"id":"m","content":[],"usage":{"input_tokens":1,"output_tokens":1,
		"cache_creation_input_tokens":0,
		"cache_creation":{"ephemeral_5m_input_tokens":9000,"ephemeral_1h_input_tokens":0}}}`
	var r apiResponse
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	u := r.toResponse().Usage
	if u.CacheWrite5mTokens != 0 || u.CacheWrite1hTokens != 0 {
		t.Errorf("split = 5m %d / 1h %d, want 0/0 — an impossible breakdown is not a measurement",
			u.CacheWrite5mTokens, u.CacheWrite1hTokens)
	}
}
