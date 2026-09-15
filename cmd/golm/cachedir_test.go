// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// GOLM_CACHE is documented in the README and in --help.
func TestCacheDirHonoursGOLMCACHE(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOLM_CACHE", dir)
	got := cacheDir()
	if got == "" {
		t.Fatal("GOLM_CACHE was set and cacheDir() returned nothing")
	}
	if !strings.HasPrefix(got, dir) {
		t.Errorf("cacheDir() = %q, want it under %q", got, dir)
	}
}

// Unset, it falls back to the user's cache directory rather than to nothing.
func TestCacheDirFallsBackToTheHomeCache(t *testing.T) {
	t.Setenv("GOLM_CACHE", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory on this host")
	}
	got := cacheDir()
	want := filepath.Join(home, ".cache", "golm")
	if !strings.HasPrefix(got, want) {
		t.Errorf("cacheDir() = %q, want it under %q", got, want)
	}
}

// An empty value is not a setting.
func TestEmptyCacheEnvIsNotASetting(t *testing.T) {
	t.Setenv("GOLM_CACHE", "")
	if cacheDir() == "" {
		if _, err := os.UserHomeDir(); err == nil {
			t.Error("an empty GOLM_CACHE disabled the cache instead of falling back")
		}
	}
}
