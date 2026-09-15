// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leelsey/golm"
)

func TestEnvName(t *testing.T) {
	cases := map[string]string{
		"model":            "GOLM_MODEL",
		"max-total-tokens": "GOLM_MAX_TOTAL_TOKENS",
		"fetch-internal":   "GOLM_FETCH_INTERNAL",
		"allow-run":        "GOLM_ALLOW_RUN",
		"store":            "GOLM_SESSIONS",
	}
	for flagName, want := range cases {
		got, ok := envName("", flagName)
		if !ok || got != want {
			t.Errorf("envName(%q) = %q,%v; want %q", flagName, got, ok, want)
		}
	}

	for _, skipped := range []string{"m", "V", "config", "version"} {
		if _, ok := envName("", skipped); ok {
			t.Errorf("envName(%q) should have no variable of its own", skipped)
		}
	}
}

// The command line always wins.
func TestFlagBeatsEnvironment(t *testing.T) {
	t.Setenv("GOLM_MODEL", "from-env")
	t.Setenv("GOLM_MAX_TOKENS", "111")
	t.Setenv("GOLM_EFFORT", "low")

	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	bf := addBackendFlags(fs)
	if code := parseWithEnv(fs, []string{"--model", "from-flag", "--max-tokens", "999"}, io.Discard); code >= 0 {
		t.Fatalf("parse = %d", code)
	}
	if bf.model != "from-flag" {
		t.Errorf("model = %q, want the flag to win", bf.model)
	}
	if bf.maxTokens != 999 {
		t.Errorf("maxTokens = %d, want the flag to win", bf.maxTokens)
	}

	if bf.effort != "low" {
		t.Errorf("effort = %q, want it filled in from the environment", bf.effort)
	}
}

func TestEnvFillsEveryKind(t *testing.T) {
	t.Setenv("GOLM_MODEL", "claude-sonnet-4-6")
	t.Setenv("GOLM_MAX_STEPS", "12")
	t.Setenv("GOLM_TIMEOUT", "90s")
	t.Setenv("GOLM_FETCH", "yes")
	t.Setenv("GOLM_READ_ONLY", "1")
	t.Setenv("GOLM_ALLOW_RUN", "git, go ,  ")
	t.Setenv("GOLM_SKILLS", "/a,/b")
	t.Setenv("GOLM_SESSIONS", "/tmp/store")

	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	bf := addBackendFlags(fs)
	if code := parseWithEnv(fs, nil, io.Discard); code >= 0 {
		t.Fatalf("parse = %d", code)
	}
	if bf.model != "claude-sonnet-4-6" {
		t.Errorf("model = %q", bf.model)
	}
	if bf.maxSteps != 12 {
		t.Errorf("maxSteps = %d", bf.maxSteps)
	}
	if bf.timeout != 90*time.Second {
		t.Errorf("timeout = %v", bf.timeout)
	}
	if !bf.fetch {
		t.Error(`GOLM_FETCH=yes should read as true; an environment variable is written by hand`)
	}
	if !bf.readOnly {
		t.Error("GOLM_READ_ONLY=1 should read as true")
	}

	if len(bf.allowRun) != 2 || bf.allowRun[0] != "git" || bf.allowRun[1] != "go" {
		t.Errorf("allowRun = %v, want the list split and trimmed", bf.allowRun)
	}
	if len(bf.skills) != 2 {
		t.Errorf("skills = %v, want two directories", bf.skills)
	}
	if bf.store != "/tmp/store" {
		t.Errorf("store = %q, want it from GOLM_SESSIONS", bf.store)
	}
}

func TestEnvBooleanForms(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{"true", true}, {"1", true}, {"yes", true}, {"YES", true}, {"on", true}, {"y", true},
		{"false", false}, {"0", false}, {"no", false}, {"off", false}, {"n", false},
	} {
		t.Setenv("GOLM_FETCH", tc.raw)
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		bf := addBackendFlags(fs)
		if code := parseWithEnv(fs, nil, io.Discard); code >= 0 {
			t.Fatalf("GOLM_FETCH=%q: parse = %d", tc.raw, code)
		}
		if bf.fetch != tc.want {
			t.Errorf("GOLM_FETCH=%q gave %v, want %v", tc.raw, bf.fetch, tc.want)
		}
	}
}

// A variable that cannot be parsed is an error naming it, not a setting that quietly does nothing.
func TestBadEnvValueIsReported(t *testing.T) {
	t.Setenv("GOLM_MAX_STEPS", "several")
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addBackendFlags(fs)
	var errOut bytes.Buffer
	if code := parseWithEnv(fs, nil, &errOut); code != 2 {
		t.Fatalf("parse = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "GOLM_MAX_STEPS") || !strings.Contains(errOut.String(), "--max-steps") {
		t.Errorf("stderr = %q, want the variable and the flag it feeds both named", errOut.String())
	}
	if !strings.Contains(errOut.String(), "several") {
		t.Errorf("stderr = %q, want the offending value shown", errOut.String())
	}
}

func TestEmptyEnvIsNotASetting(t *testing.T) {
	t.Setenv("GOLM_MODEL", "")
	t.Setenv("GOLM_EFFORT", "   ")
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	bf := addBackendFlags(fs)
	if code := parseWithEnv(fs, nil, io.Discard); code >= 0 {
		t.Fatalf("parse = %d", code)
	}
	if bf.model != "" || bf.effort != "" {
		t.Errorf("model = %q, effort = %q; an empty variable should not count as set", bf.model, bf.effort)
	}
}

// GOLM_CONFIG keeps the semantics it already had.
func TestConfigEnvDoesNotHijackAnAdHocRun(t *testing.T) {
	cfg := cliConfig(t)
	t.Setenv("GOLM_CONFIG", cfg)

	var got string
	srv := httptest.NewServer(recordModel(&got))
	defer srv.Close()
	t.Setenv("GOLM_TEST_KEY", "k")

	var out, errOut bytes.Buffer
	args := []string{
		"--provider", "openai", "--base-url", srv.URL + "/v1",
		"--api-key-env", "GOLM_TEST_KEY", "--model", "gpt-4o", "--no-stream", "hello",
	}
	if code := run(args, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("run = %d: %s", code, errOut.String())
	}
	if got != "gpt-4o" {
		t.Errorf("model on the wire = %q, want the flag's; GOLM_CONFIG must not take over an ad-hoc run", got)
	}

	out.Reset()
	errOut.Reset()
	if code := run([]string{"--no-stream", "hello"}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("discovered run = %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "hello") {
		t.Errorf("output = %q, want the cat-backed config to have answered", out.String())
	}
}

// Subcommands read the environment too.
func TestSubcommandsReadTheEnvironment(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOLM_SESSIONS", dir)
	var out, errOut bytes.Buffer
	if code := runSessions([]string{"list"}, &out, &errOut); code != 0 {
		t.Fatalf("sessions list = %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "no sessions") {
		t.Errorf("output = %q, want the empty store from GOLM_SESSIONS", out.String())
	}
}

func recordModel(dst *string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req struct {
			Model string `json:"model"`
		}
		json.Unmarshal(b, &req)
		*dst = req.Model
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"hello"},"finish_reason":"stop"}]}`)
	})
}

// A shorthand is the same setting as its long form.
func TestShorthandFlagBeatsEnvironment(t *testing.T) {
	t.Setenv("GOLM_MODEL", "from-env")
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	bf := addBackendFlags(fs)
	if code := parseWithEnv(fs, []string{"-m", "from-flag"}, io.Discard); code >= 0 {
		t.Fatalf("parse = %d", code)
	}
	if bf.model != "from-flag" {
		t.Errorf("model = %q, want the shorthand on the command line to win", bf.model)
	}
}

// A generic flag name and a broad variable are a bad pair.
func TestSubcommandScopedNames(t *testing.T) {
	cases := []struct{ scope, flag, want string }{
		{"bus", "addr", "GOLM_BUS_ADDR"},
		{"a2a", "addr", "GOLM_A2A_ADDR"},
		{"mcp", "name", "GOLM_MCP_NAME"},
		{"sessions", "limit", "GOLM_SESSIONS_LIMIT"},
		{"sessions", "store", "GOLM_SESSIONS"},
	}
	for _, tc := range cases {
		got, ok := envName(tc.scope, tc.flag)
		if !ok || got != tc.want {
			t.Errorf("envName(%q, %q) = %q,%v; want %q", tc.scope, tc.flag, got, ok, tc.want)
		}
	}

	bus, _ := envName("bus", "addr")
	a2a, _ := envName("a2a", "addr")
	if bus == a2a {
		t.Error("the bus and a2a listen addresses must not share a variable")
	}
}

// A config mutation says what to WRITE.
func TestConfigMutationsIgnoreTheEnvironment(t *testing.T) {
	t.Setenv("GOLM_NAME", "sneaky")
	t.Setenv("GOLM_CONFIG_NAME", "also-sneaky")
	p := filepath.Join(t.TempDir(), "c.json")
	if code := runArgs("config", "add-provider", "--config", p, "--type", "cli", "--command", "cat"); code == 0 {
		cfg, err := golm.LoadConfig(p)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		for _, pr := range cfg.Providers {
			if pr.Name == "sneaky" || pr.Name == "also-sneaky" {
				t.Fatalf("a provider was written under a name from the environment: %q", pr.Name)
			}
		}
	}

	if code := runArgs("config", "add-provider", "--config", p, "--type", "cli", "--command", "cat"); code == 0 {
		t.Error("add-provider with no --name should fail")
	}
}
