// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package proc

import (
	"runtime"
	"strings"
	"testing"
)

// The property the whole helper exists for.
func TestMinimalEnvOmitsTheParentsSecrets(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-should-not-travel")
	t.Setenv("OPENAI_API_KEY", "sk-oai-should-not-travel")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "should-not-travel")

	env := MinimalEnv(nil)
	for _, e := range env {
		if strings.Contains(e, "should-not-travel") {
			t.Fatalf("a credential reached the child environment: %q", e)
		}
	}

	if !hasKey(env, "PATH") {
		t.Error("PATH is missing; a child with no PATH cannot find a program")
	}
	if runtime.GOOS == "windows" && !hasKey(env, "SystemRoot") {
		t.Error("SystemRoot is missing; a Windows process fails to initialise without it")
	}
}

func TestMinimalEnvInheritsByName(t *testing.T) {
	t.Setenv("GOLM_PROC_TEST_TOKEN", "wanted")
	env := MinimalEnv([]string{"GOLM_PROC_TEST_TOKEN"})
	if v, ok := lookup(env, "GOLM_PROC_TEST_TOKEN"); !ok || v != "wanted" {
		t.Fatalf("named variable not passed through: got %q ok=%v", v, ok)
	}
}

// An unset name must be skipped, not passed through empty.
func TestMinimalEnvSkipsNamesThatAreUnset(t *testing.T) {
	env := MinimalEnv([]string{"GOLM_PROC_TEST_DEFINITELY_UNSET"})
	if _, ok := lookup(env, "GOLM_PROC_TEST_DEFINITELY_UNSET"); ok {
		t.Fatal("an unset name was passed through as an empty value")
	}
}

// Naming something already in the base set must not produce it twice.
func TestMinimalEnvDoesNotDuplicate(t *testing.T) {
	env := MinimalEnv([]string{"PATH", "PATH"})
	if n := count(env, "PATH"); n != 1 {
		t.Fatalf("PATH appears %d times, want 1", n)
	}
}

func TestMinimalEnvIgnoresAnEmptyName(t *testing.T) {
	for _, e := range MinimalEnv([]string{""}) {
		if strings.HasPrefix(e, "=") {
			t.Fatalf("an empty name produced an entry: %q", e)
		}
	}
}

func hasKey(env []string, k string) bool { _, ok := lookup(env, k); return ok }

func lookup(env []string, k string) (string, bool) {
	for _, e := range env {
		if name, v, ok := strings.Cut(e, "="); ok && name == k {
			return v, true
		}
	}
	return "", false
}

func count(env []string, k string) int {
	var n int
	for _, e := range env {
		if name, _, ok := strings.Cut(e, "="); ok && name == k {
			n++
		}
	}
	return n
}
