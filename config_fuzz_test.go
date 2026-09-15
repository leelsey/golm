// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/leelsey/golm"
)

// A config decides which provider is reached.
func FuzzLoadConfig(f *testing.F) {
	f.Add(`{"providers":[{"name":"p","type":"openai","api_key_env":"K"}],
	        "agents":[{"name":"a","provider":"p","model":"m"}]}`)
	f.Add(`{}`)
	f.Add(`{"providers":null,"agents":null}`)
	f.Add(`{"agents":[{"name":"a","provider":"missing","model":"m"}]}`)
	f.Add(`{"providers":[{"name":"p","type":"nonsense"}]}`)
	f.Add(`{"agents":[{"name":"a","provider":"p","model":"m","keep_last":3,
	        "compaction":{"at_messages":10}}],"providers":[{"name":"p","type":"cli","command":"/bin/cat"}]}`)
	f.Add(`{"orchestration":{"routes":[{"agent":"nobody","any":["x"]}]}}`)
	f.Add(`{"mcp_servers":[{"name":"m","command":"x","inherit_env":["A"]}]}`)
	f.Add(`not json at all`)
	f.Add(`[]`)

	dir := f.TempDir()
	p := filepath.Join(dir, "golm.json")

	f.Fuzz(func(t *testing.T, body string) {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Skip()
		}
		cfg, err := golm.LoadConfig(p)
		if err != nil {
			return
		}
		if cfg == nil {
			t.Fatalf("LoadConfig returned no error and no config for %q", body)
		}

		known := map[string]bool{}
		for _, pr := range cfg.Providers {
			if pr.Name == "" {
				t.Fatalf("accepted a provider with no name: %q", body)
			}
			known[pr.Name] = true
		}
		seen := map[string]bool{}
		for _, a := range cfg.Agents {
			if a.Name == "" {
				t.Fatalf("accepted an agent with no name: %q", body)
			}
			if seen[a.Name] {
				t.Fatalf("accepted two agents named %q: %q", a.Name, body)
			}
			seen[a.Name] = true
			if !known[a.Provider] {
				t.Fatalf("agent %q names provider %q, which is not declared: %q", a.Name, a.Provider, body)
			}
		}

		out := filepath.Join(dir, "round.json")
		if err := cfg.Save(out); err != nil {
			t.Fatalf("a loaded config will not save: %v (%q)", err, body)
		}
		if _, err := golm.LoadConfig(out); err != nil {
			t.Fatalf("a saved config will not load: %v (%q)", err, body)
		}
	})
}
