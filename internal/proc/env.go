// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package proc

import (
	"os"
	"runtime"
	"strings"
)

// MinimalEnv is the environment a child process is given when its caller has not supplied one.
func MinimalEnv(inherit []string) []string {
	base := baseEnvKeys()
	out := make([]string, 0, len(base)+len(inherit))
	seen := make(map[string]bool, len(base)+len(inherit))
	add := func(k string) {
		if k == "" {
			return
		}

		key := k
		if runtime.GOOS == "windows" {
			key = strings.ToUpper(k)
		}
		if seen[key] {
			return
		}
		seen[key] = true
		if v, ok := os.LookupEnv(k); ok {
			out = append(out, k+"="+v)
		}
	}
	for _, k := range base {
		add(k)
	}
	for _, k := range inherit {
		add(k)
	}
	return out
}

func baseEnvKeys() []string {
	if runtime.GOOS == "windows" {
		return []string{
			"SystemRoot", "SystemDrive", "windir",
			"PATH", "PATHEXT", "COMSPEC",
			"TEMP", "TMP",
			"USERPROFILE", "HOMEDRIVE", "HOMEPATH",
			"APPDATA", "LOCALAPPDATA", "ProgramData",
			"NUMBER_OF_PROCESSORS",
		}
	}
	return []string{"PATH", "HOME", "LANG", "LC_ALL", "TMPDIR", "TZ"}
}
