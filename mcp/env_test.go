// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

var leakProbes = map[string]string{
	"ANTHROPIC_API_KEY":     "sk-ant-LEAKED",
	"OPENAI_API_KEY":        "sk-oai-LEAKED",
	"GOOGLE_API_KEY":        "goog-LEAKED",
	"AWS_SECRET_ACCESS_KEY": "aws-LEAKED",
	"GITHUB_TOKEN":          "ghp-LEAKED",
}

// An MCP server is a third-party program.
func TestMCPServerCannotReadTheHostsCredentials(t *testing.T) {
	r := envProbe(t)
	defer r.Close()

	for name := range leakProbes {
		if got := probeEnv(t, r, name); got != "" {
			t.Errorf("the server read %s = %q — the host's environment is reaching a third-party subprocess", name, got)
		}
	}
}

// The minimal environment still has to be a WORKING one, or every deployment reaches for Env.
func TestMCPServerStillGetsAUsableEnvironment(t *testing.T) {
	r := envProbe(t)
	defer r.Close()
	if probeEnv(t, r, "PATH") == "" {
		t.Error("the server has no PATH; it cannot find a program it needs")
	}
}

// What a server legitimately needs, it is given BY NAME.
func TestMCPServerReceivesNamedVariables(t *testing.T) {
	t.Setenv("GOLM_MCP_SERVER_TOKEN", "wanted-by-this-server")
	r := envProbe(t, "GOLM_MCP_SERVER_TOKEN")
	defer r.Close()
	if got := probeEnv(t, r, "GOLM_MCP_SERVER_TOKEN"); got != "wanted-by-this-server" {
		t.Errorf("named variable = %q, want it passed through", got)
	}
	if got := probeEnv(t, r, "ANTHROPIC_API_KEY"); got != "" {
		t.Errorf("naming one variable also passed ANTHROPIC_API_KEY = %q", got)
	}
}

// Env set explicitly is the caller taking the decision.
func TestMCPServerEnvVerbatimIsHonoured(t *testing.T) {
	r := newProbeRemote(t)
	r.Inherit = nil
	r.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		serverEnv + "=1", envProbeEnv + "=1",
		"ONLY_THIS=yes",
	}
	defer r.Close()
	if got := probeEnv(t, r, "ONLY_THIS"); got != "yes" {
		t.Errorf("verbatim Env not used: ONLY_THIS = %q", got)
	}
	if got := probeEnv(t, r, "ANTHROPIC_API_KEY"); got != "" {
		t.Errorf("verbatim Env still leaked ANTHROPIC_API_KEY = %q", got)
	}
}

func envProbe(t *testing.T, alsoInherit ...string) *Remote {
	t.Helper()
	r := newProbeRemote(t)
	r.Inherit = append([]string{serverEnv, envProbeEnv}, alsoInherit...)
	return r
}

func newProbeRemote(t *testing.T) *Remote {
	t.Helper()
	for k, v := range leakProbes {
		t.Setenv(k, v)
	}
	t.Setenv(serverEnv, "1")
	t.Setenv(envProbeEnv, "1")
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}
	return &Remote{Name: "envprobe", Command: self, CacheDir: t.TempDir(),
		Inherit: []string{serverEnv, envProbeEnv}}
}

func probeEnv(t *testing.T, r *Remote, name string) string {
	t.Helper()
	tools, err := r.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name() != "getenv" {
		t.Fatalf("tools = %v, want the getenv probe", tools)
	}
	out, err := tools[0].Execute(context.Background(), []byte(`{"name":"`+name+`"}`))
	if err != nil {
		t.Fatalf("Execute(%s): %v", name, err)
	}
	var b strings.Builder
	for _, c := range out {
		if tx, ok := c.(golm.Text); ok {
			b.WriteString(tx.Text)
		}
	}
	return strings.TrimSpace(b.String())
}
