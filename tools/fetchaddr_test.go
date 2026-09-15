// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package tools

import (
	"net"
	"strings"
	"testing"
)

func TestBlockedIPRanges(t *testing.T) {
	cases := []struct {
		ip      string
		private bool
		blocked bool
		why     string
	}{
		{"93.184.216.34", false, false, ""},
		{"2606:2800:220:1::1", false, false, ""},

		{"127.0.0.1", false, true, "loopback"},
		{"::1", false, true, "loopback"},
		{"169.254.169.254", false, true, "link-local"},
		{"fd00::1", false, true, "private"},
		{"10.0.0.1", false, true, "private"},
		{"192.168.1.1", false, true, "private"},
		{"0.0.0.0", false, true, "unspecified"},
		{"224.0.0.1", false, true, "multicast"},

		{"100.64.0.1", false, true, "reserved"},
		{"100.64.0.1", true, true, "reserved"},
		{"198.18.0.1", true, true, "reserved"},
		{"192.0.2.1", true, true, "reserved"},
		{"203.0.113.9", true, true, "reserved"},
		{"240.0.0.1", true, true, "reserved"},
		{"255.255.255.255", true, true, "reserved"},
		{"2001:db8::1", true, true, "reserved"},

		{"127.0.0.1", true, false, ""},
		{"10.0.0.1", true, false, ""},
		{"169.254.169.254", true, false, ""},
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if ip == nil {
			t.Fatalf("bad test address %q", c.ip)
		}
		why := blockedIP(ip, c.private)
		if (why != "") != c.blocked {
			t.Errorf("blockedIP(%s, allowPrivate=%v) = %q, blocked=%v want %v", c.ip, c.private, why, why != "", c.blocked)
			continue
		}
		if c.why != "" && !strings.Contains(why, c.why) {
			t.Errorf("blockedIP(%s) = %q, want a reason mentioning %q", c.ip, why, c.why)
		}
	}
}

// An attacker who chooses the ENCODING of an address chooses which of your checks apply.
func TestTransitionalAddressesAreUnwrapped(t *testing.T) {
	cases := []struct {
		name string
		ip   string
	}{
		{"IPv4-mapped loopback", "::ffff:127.0.0.1"},
		{"IPv4-mapped metadata", "::ffff:169.254.169.254"},
		{"NAT64 loopback", "64:ff9b::7f00:1"},
		{"NAT64 metadata", "64:ff9b::a9fe:a9fe"},
		{"6to4 loopback", "2002:7f00:1::1"},
		{"6to4 private", "2002:0a00:0001::1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ip := net.ParseIP(c.ip)
			if ip == nil {
				t.Fatalf("bad test address %q", c.ip)
			}
			if why := blockedIP(ip, false); why == "" {
				t.Errorf("%s (%s) was allowed", c.name, c.ip)
			}
		})
	}

	if why := blockedIP(net.ParseIP("2002:5db8:d822::1"), false); why != "" {
		t.Errorf("6to4 over a public address was blocked: %s", why)
	}
}

func TestBlockedIPRejectsNonsense(t *testing.T) {
	if why := blockedIP(nil, true); why == "" {
		t.Error("a nil address was allowed")
	}
	if why := blockedIP(net.IP{1, 2, 3}, true); why == "" {
		t.Error("a malformed address was allowed")
	}
}
