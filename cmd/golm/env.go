// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

const envPrefix = "GOLM_"

var flagAliases = map[string]string{"m": "model", "V": "version"}

func envName(scope, name string) (string, bool) {
	switch name {
	case "config", "version":
		return "", false
	case "store":

		return envPrefix + "SESSIONS", true
	}
	if len(name) < 2 {
		return "", false
	}
	key := envPrefix
	if scope != "" {
		key += strings.ToUpper(strings.ReplaceAll(scope, "-", "_")) + "_"
	}
	return key + strings.ToUpper(strings.ReplaceAll(name, "-", "_")), true
}

func applyEnv(fs *flag.FlagSet, scope string) error {
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) {
		given[f.Name] = true

		if long, ok := flagAliases[f.Name]; ok {
			given[long] = true
		}
	})

	var firstErr error
	fs.VisitAll(func(f *flag.Flag) {
		if firstErr != nil || given[f.Name] {
			return
		}
		key, ok := envName(scope, f.Name)
		if !ok {
			return
		}
		raw, ok := os.LookupEnv(key)
		if !ok || strings.TrimSpace(raw) == "" {
			return
		}
		for _, v := range envValues(f, raw) {
			if err := f.Value.Set(v); err != nil {
				firstErr = fmt.Errorf("%s=%q (--%s): %w", key, raw, f.Name, err)
				return
			}
		}
	})
	return firstErr
}

func envValues(f *flag.Flag, raw string) []string {
	if _, ok := f.Value.(*stringList); ok {
		var out []string
		for _, part := range strings.Split(raw, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
		return out
	}
	if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "yes", "y", "on":
			return []string{"true"}
		case "no", "n", "off":
			return []string{"false"}
		}
	}
	return []string{strings.TrimSpace(raw)}
}

func parseWithEnv(fs *flag.FlagSet, args []string, stderr io.Writer) int {
	return parseScoped(fs, args, stderr, "")
}

func parseScoped(fs *flag.FlagSet, args []string, stderr io.Writer, scope string) int {
	if code := parsePlain(fs, args); code >= 0 {
		return code
	}
	if err := applyEnv(fs, scope); err != nil {
		fmt.Fprintln(stderr, "golm:", err)
		return 2
	}
	return -1
}

func parsePlain(fs *flag.FlagSet, args []string) int {
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	return -1
}
