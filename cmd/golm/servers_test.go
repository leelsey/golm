// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// Every served surface refuses to start rather than start wrong.
func TestServerSubcommandsRefuseAnEmptyToken(t *testing.T) {
	cfg := writeCfg(t, `{
      "providers":[{"name":"cat","type":"cli","command":"/bin/cat"}],
      "agents":[{"name":"main","provider":"cat","model":"cat","role":"main"}]}`)
	t.Setenv("GOLM_TEST_TOKEN_ABSENT", "")

	for _, c := range []struct {
		name string
		args []string
	}{
		{"a2a-serve", []string{"--config", cfg, "--token-env", "GOLM_TEST_TOKEN_ABSENT"}},
		{"bus", []string{"--token-env", "GOLM_TEST_TOKEN_ABSENT"}},
	} {
		var errb strings.Builder
		var code int
		switch c.name {
		case "a2a-serve":
			code = runA2AServe(c.args, &errb)
		case "bus":
			code = runBus(c.args, &errb)
		}
		if code == 0 {
			t.Errorf("%s started with an empty token", c.name)
		}
		if !strings.Contains(errb.String(), "token-env") {
			t.Errorf("%s: the error does not name the flag: %q", c.name, errb.String())
		}
	}
}

func TestA2AServeRequiresAConfig(t *testing.T) {
	var errb strings.Builder
	if code := runA2AServe(nil, &errb); code == 0 {
		t.Fatal("a2a-serve started with no --config")
	}
	if !strings.Contains(errb.String(), "--config") {
		t.Errorf("error = %q", errb.String())
	}
}

func TestA2AServeReportsAnUnreadableConfig(t *testing.T) {
	var errb strings.Builder
	code := runA2AServe([]string{"--config", filepath.Join(t.TempDir(), "absent.json")}, &errb)
	if code == 0 {
		t.Fatal("a2a-serve started with a missing config")
	}
	if errb.Len() == 0 {
		t.Error("nothing was reported")
	}
}

// A config with no agents has nothing to expose.
func TestA2AServeRefusesWhenThereIsNoAgent(t *testing.T) {
	cfg := writeCfg(t, `{"providers":[],"agents":[]}`)
	var errb strings.Builder
	if code := runA2AServe([]string{"--config", cfg}, &errb); code == 0 {
		t.Fatal("a2a-serve started with no agent to expose")
	}
	if !strings.Contains(errb.String(), "no agent") {
		t.Errorf("error = %q", errb.String())
	}
}

func TestMCPServerReportsAnUnreadableConfig(t *testing.T) {
	var errb strings.Builder
	code := runMCPServer([]string{"--config", filepath.Join(t.TempDir(), "absent.json")}, &errb)
	if code == 0 {
		t.Fatal("mcp-server started with a missing config")
	}
}

func TestACPReportsAnUnreadableConfig(t *testing.T) {
	var errb strings.Builder
	if code := runACP([]string{"--config", filepath.Join(t.TempDir(), "absent.json")}, &errb); code == 0 {
		t.Fatal("acp started with a missing config")
	}
}

// The loopback warning is the only thing standing between a default-local listener.
func TestWarnOpenListener(t *testing.T) {
	for _, c := range []struct{ addr, token, want string }{
		{"127.0.0.1:8080", "", ""},
		{"localhost:8080", "", ""},
		{"[::1]:8080", "", ""},
		{":8080", "", "without authentication"},
		{"0.0.0.0:8080", "", "without authentication"},
		{"0.0.0.0:8080", "secret", "clear"},
		{"192.168.1.5:8080", "secret", "TLS"},
	} {
		var out strings.Builder
		warnOpenListener(&out, "bus", c.addr, c.token, "risky")
		got := out.String()
		if c.want == "" {
			if got != "" {
				t.Errorf("%s (token=%q) warned when it should not: %q", c.addr, c.token, got)
			}
			continue
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("%s (token=%q) = %q, want it to mention %q", c.addr, c.token, got, c.want)
		}
	}
}

// A token on a non-loopback address still warns.
func TestATokenDoesNotSilenceTheOpenListenerWarning(t *testing.T) {
	var out strings.Builder
	warnOpenListener(&out, "a2a-serve", "0.0.0.0:8080", "t0ken", "risky")
	if out.Len() == 0 {
		t.Fatal("a token silenced the warning; the token itself crosses in clear")
	}
	if strings.Contains(out.String(), "t0ken") {
		t.Error("the warning printed the token")
	}
}

func TestSubcommandsRejectAnUnknownFlag(t *testing.T) {
	for name, fn := range map[string]func([]string, *strings.Builder) int{
		"bus":        func(a []string, e *strings.Builder) int { return runBus(a, e) },
		"a2a-serve":  func(a []string, e *strings.Builder) int { return runA2AServe(a, e) },
		"mcp-server": func(a []string, e *strings.Builder) int { return runMCPServer(a, e) },
		"acp":        func(a []string, e *strings.Builder) int { return runACP(a, e) },
	} {
		var errb strings.Builder
		if code := fn([]string{"--definitely-not-a-flag"}, &errb); code == 0 {
			t.Errorf("%s accepted an unknown flag", name)
		}
	}
}
