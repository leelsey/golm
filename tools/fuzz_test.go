// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package tools

import (
	"context"
	"encoding/json"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

// The workspace is the only thing between a model-chosen path and the rest of the filesystem.
func FuzzWorkspaceResolveNeverEscapes(f *testing.F) {
	f.Add("notes.txt")
	f.Add("./a/../b")
	f.Add("../outside")
	f.Add("a/../../outside")
	f.Add("/etc/passwd")
	f.Add("a//b///c")
	f.Add("....//....//etc")
	f.Add(".")
	f.Add("")
	f.Add("a\\..\\..\\b")
	f.Add(strings.Repeat("a/", 200) + "f")
	f.Add("\x00etc/passwd")

	f.Fuzz(func(t *testing.T, p string) {
		dir := t.TempDir()
		w, err := Open(dir)
		if err != nil {
			t.Skip()
		}
		defer w.Close()

		got, err := w.resolve(p)
		if err != nil {
			return
		}

		if filepath.IsAbs(got) {
			t.Fatalf("resolve(%q) returned the absolute path %q", p, got)
		}
		if got == ".." || strings.HasPrefix(got, "../") {
			t.Fatalf("resolve(%q) returned %q, which climbs out", p, got)
		}

		joined := filepath.Clean(filepath.Join(dir, got))
		if joined != filepath.Clean(dir) && !strings.HasPrefix(joined, filepath.Clean(dir)+string(filepath.Separator)) {
			t.Fatalf("resolve(%q) = %q joins to %q, outside %q", p, got, joined, dir)
		}
	})
}

// The same property through the TOOL.
func FuzzWorkspaceReadStaysInside(f *testing.F) {
	f.Add("inside.txt")
	f.Add("../secret.txt")
	f.Add("/etc/hosts")
	f.Add("sub/../../secret.txt")

	f.Fuzz(func(t *testing.T, p string) {
		root := t.TempDir()
		inside := filepath.Join(root, "ws")
		if err := os.MkdirAll(filepath.Join(inside, "sub"), 0o755); err != nil {
			t.Skip()
		}
		if err := os.WriteFile(filepath.Join(inside, "inside.txt"), []byte("in"), 0o600); err != nil {
			t.Skip()
		}

		secret := filepath.Join(root, "secret.txt")
		if err := os.WriteFile(secret, []byte("SECRET-VALUE"), 0o600); err != nil {
			t.Skip()
		}

		w, err := Open(inside)
		if err != nil {
			t.Skip()
		}
		defer w.Close()

		var read interface {
			Name() string
			Execute(context.Context, json.RawMessage) ([]golm.ToolContent, error)
		}
		for _, tl := range w.Tools() {
			if tl.Name() == "read_file" {
				read = tl
			}
		}
		if read == nil {
			t.Skip()
		}
		args, _ := json.Marshal(map[string]string{"path": p})
		out, err := read.Execute(context.Background(), args)
		if err != nil {
			return
		}
		var b strings.Builder
		for _, c := range out {
			if tx, ok := c.(interface{ String() string }); ok {
				b.WriteString(tx.String())
			}
		}
		text := b.String()
		if strings.Contains(text, "SECRET-VALUE") {
			t.Fatalf("read_file(%q) returned a file outside the workspace", p)
		}
	})
}

// An attacker choosing the ENCODING of an address is choosing which checks apply.
func FuzzBlockedIPIgnoresTheEncoding(f *testing.F) {
	f.Add([]byte{127, 0, 0, 1})
	f.Add([]byte{8, 8, 8, 8})
	f.Add([]byte{169, 254, 1, 1})
	f.Add([]byte{100, 64, 0, 1})
	f.Add([]byte(net.ParseIP("::1").To16()))
	f.Add([]byte(net.ParseIP("2002:7f00:1::").To16()))
	f.Add([]byte(net.ParseIP("64:ff9b::7f00:1").To16()))
	f.Add([]byte(net.ParseIP("::ffff:10.0.0.1").To16()))

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) != 4 && len(raw) != 16 {
			t.Skip()
		}
		ip := net.IP(raw)
		a, ok := netip.AddrFromSlice(raw)
		if !ok {
			t.Skip()
		}
		why := blockedIP(ip, false)

		targets := []netip.Addr{a.Unmap()}
		if inner, ok := v4Embedded(a); ok {
			targets = append(targets, inner)
		}
		for _, tgt := range targets {
			if mustBeRefused(tgt) && why == "" {
				t.Fatalf("%v (reaching %v) was allowed; encoding chose which checks applied", a, tgt)
			}
		}

		if why != "" && strings.TrimSpace(why) == "" {
			t.Fatalf("%v refused with an empty reason", a)
		}
	})
}

func mustBeRefused(a netip.Addr) bool {
	if !a.IsValid() {
		return true
	}
	if a.IsUnspecified() || a.IsLoopback() || a.IsMulticast() ||
		a.IsInterfaceLocalMulticast() || a.IsLinkLocalMulticast() ||
		a.IsLinkLocalUnicast() || a.IsPrivate() {
		return true
	}
	for _, p := range specialPurpose {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
