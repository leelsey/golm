// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"runtime/debug"
	"testing"
)

// A release stamps Version with -ldflags.
func TestModuleVersion(t *testing.T) {
	cases := []struct {
		name string
		bi   *debug.BuildInfo
		want string
	}{
		{
			name: "golm is the main module",
			bi:   &debug.BuildInfo{Main: debug.Module{Path: modulePath, Version: "v0.1.0"}},
			want: "v0.1.0",
		},
		{
			name: "embedded in another program",
			bi: &debug.BuildInfo{
				Main: debug.Module{Path: "example.com/app", Version: "v9.9.9"},
				Deps: []*debug.Module{
					{Path: "example.com/other", Version: "v2.0.0"},
					{Path: modulePath, Version: "v0.1.0"},
				},
			},
			want: "v0.1.0",
		},
		{
			name: "a replace directive names the version actually built",
			bi: &debug.BuildInfo{
				Main: debug.Module{Path: "example.com/app", Version: "v9.9.9"},
				Deps: []*debug.Module{{
					Path:    modulePath,
					Version: "v0.1.0",
					Replace: &debug.Module{Path: modulePath, Version: "v0.2.0"},
				}},
			},
			want: "v0.2.0",
		},
		{
			name: "a nested module is the main module",
			bi: &debug.BuildInfo{
				Main: debug.Module{Path: modulePath + "/sessionstore/fts", Version: "v0.3.0"},
				Deps: []*debug.Module{{Path: modulePath, Version: "v0.1.0"}},
			},
			want: "v0.1.0",
		},
		{
			name: "an unversioned build says nothing",
			bi:   &debug.BuildInfo{Main: debug.Module{Path: modulePath, Version: "(devel)"}},
			want: "",
		},
		{
			name: "golm is nowhere in the build info",
			bi:   &debug.BuildInfo{Main: debug.Module{Path: "example.com/app", Version: "v9.9.9"}},
			want: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := moduleVersion(c.bi); got != c.want {
				t.Errorf("moduleVersion = %q, want %q", got, c.want)
			}
		})
	}
}

// Whatever the build, Version must be a usable string.
func TestVersionIsNeverEmpty(t *testing.T) {
	if Version == "" {
		t.Error("Version is empty")
	}
}
