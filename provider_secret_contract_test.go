// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every adapter that puts a server's own words into an error must scrub its credentials out of them first.
func TestEveryAdapterScrubsItsCredentials(t *testing.T) {
	entries, err := os.ReadDir("provider")
	if err != nil {
		t.Fatalf("read provider dir: %v", err)
	}

	exempt := map[string]string{
		"clibackend": "runs an external CLI and holds no credential of its own",
		"hermes":     "a wrapper: the adapter beneath it owns the credential",
		"internal":   "shared mechanics, not an adapter; checked separately below",
	}

	// An adapter may redact itself or delegate to httpwire.Post, which redacts
	// unconditionally — so that helper is held to the same rule first.
	shared, err := os.ReadFile(filepath.Join("provider", "internal", "httpwire", "httpwire.go"))
	if err != nil {
		t.Fatalf("read shared httpwire: %v", err)
	}
	if !strings.Contains(string(shared), "golm.Redact(") {
		t.Fatal("httpwire builds the error body for every adapter that delegates to it and must redact")
	}
	var sawAny bool
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if why, ok := exempt[e.Name()]; ok {
			t.Logf("provider %q is exempt: %s", e.Name(), why)
			continue
		}
		files, err := filepath.Glob(filepath.Join("provider", e.Name(), "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		var source strings.Builder
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			source.Write(b)
		}
		body := source.String()
		if !strings.Contains(body, "apiKey") {
			t.Logf("provider %q holds no API key", e.Name())
			continue
		}
		sawAny = true
		if !strings.Contains(body, "golm.Redact(") && !strings.Contains(body, "httpwire.Post(") {
			t.Errorf("provider %q builds errors from server text but neither calls golm.Redact nor "+
				"routes them through httpwire.Post; an endpoint that echoes the credentials it was "+
				"sent would put them in a log, a returned error and a tool result the model reads", e.Name())
		}
	}
	if !sawAny {
		t.Fatal("found no adapter holding an API key — the scan is broken, not the tree")
	}
}
