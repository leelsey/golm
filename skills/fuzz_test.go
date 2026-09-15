// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A skill's front matter decides the NAME and DESCRIPTION that go into the system prompt.
func FuzzParseFrontMatter(f *testing.F) {
	f.Add("---\nname: writer\ndescription: writes\n---\nbody")
	f.Add("---\n---\n")
	f.Add("---\nname: \n")
	f.Add("no front matter at all")
	f.Add("---\nname: a\nname: b\n---\n")
	f.Add("---\n" + strings.Repeat("k: v\n", 500) + "---\n")
	f.Add("---\nname: " + strings.Repeat("x", 10000) + "\n---\n")
	f.Add("---\nname: a\x00b\n---\n")
	f.Add("---\r\nname: crlf\r\n---\r\n")

	f.Fuzz(func(t *testing.T, body string) {
		dir := t.TempDir()
		p := filepath.Join(dir, "SKILL.md")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Skip()
		}
		sk, err := parse(p)
		if err != nil {
			return
		}

		if sk.Name == "" {
			t.Fatalf("accepted a skill with no name from %q", body)
		}
		if strings.ContainsAny(sk.Name, "\n\r") {
			t.Fatalf("skill name spans lines (%q); it would forge a prompt section", sk.Name)
		}
		if strings.ContainsAny(sk.Description, "\n\r") {
			t.Fatalf("skill description spans lines (%q)", sk.Description)
		}
	})
}
