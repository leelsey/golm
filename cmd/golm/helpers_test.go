// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/tabwriter"
	"time"

	"github.com/leelsey/golm"
)

// The hazard openStore's own comment names.
func TestOpenStoreReturnsATrulyNilInterface(t *testing.T) {
	st, err := openStore("", false)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	if st != nil {
		t.Fatalf("openStore returned a non-nil interface (%T); every `store != nil` guard downstream now passes", st)
	}
}

func TestOpenStoreUsesTheDirectoryItIsGiven(t *testing.T) {
	dir := t.TempDir()
	st, err := openStore(dir, false)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	if st == nil {
		t.Fatal("an explicit directory produced no store")
	}
	if err := st.Save(context.Background(), golm.NewSession()); err != nil {
		t.Fatalf("save: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) == 0 {
		t.Error("the store saved nothing into the directory it was given")
	}
}

// Wanted but unnamed, it falls back to the sessions directory rather than to no store at all.
func TestOpenStoreFallsBackWhenWanted(t *testing.T) {
	t.Setenv("GOLM_SESSIONS", t.TempDir())
	st, err := openStore("", true)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	if st == nil {
		t.Fatal("a wanted store resolved to nothing; --session would keep nothing")
	}
}

// Compaction archives the pre-summary transcript.
func TestAttachArchiveNeedsBothAnAgentAndAStore(t *testing.T) {
	agent := &golm.Agent{Compaction: &golm.CompactPolicy{}}
	attachArchive(agent, nil)
	if agent.Compaction.Archive != nil {
		t.Error("an Archive was wired with no store behind it")
	}
	attachArchive(nil, sessionStoreIn(t))
	agent2 := &golm.Agent{}
	attachArchive(agent2, sessionStoreIn(t))

	agent3 := &golm.Agent{Compaction: &golm.CompactPolicy{}}
	store := sessionStoreIn(t)
	attachArchive(agent3, store)
	if agent3.Compaction.Archive == nil {
		t.Fatal("no Archive was wired despite a store")
	}
	archived := golm.NewSession()
	if err := agent3.Compaction.Archive(context.Background(), archived); err != nil {
		t.Fatalf("the wired Archive failed: %v", err)
	}
	if _, err := store.Load(context.Background(), archived.ID()); err != nil {
		t.Errorf("the archive did not reach the store: %v", err)
	}
}

func sessionStoreIn(t *testing.T) golm.SessionStore {
	t.Helper()
	st, err := openStore(t.TempDir(), false)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	return st
}

// short trims an id for a listing.
func TestShortAbbreviatesOnlyWhatIsTooLong(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"abc", "abc"},
		{"12345678", "12345678"},
		{"123456789", "12345678"},
		{"0123456789abcdef", "01234567"},
	} {
		if got := short(c.in); got != c.want {
			t.Errorf("short(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// flushOr reports a write failure rather than exiting 0 on a listing the user never saw.
func TestFlushOrReportsAWriteFailure(t *testing.T) {
	var out bytes.Buffer
	tw := tabwriter.NewWriter(&out, 0, 0, 1, ' ', 0)
	if got := flushOr(tw, &out); got != 0 {
		t.Errorf("a healthy flush returned %d, want 0", got)
	}

	var errs bytes.Buffer
	bad := tabwriter.NewWriter(failingWriter{}, 0, 0, 1, ' ', 0)
	_, _ = bad.Write([]byte("something\n"))
	if got := flushOr(bad, &errs); got != 1 {
		t.Errorf("a failed flush returned %d, want 1", got)
	}
	if !strings.Contains(errs.String(), "golm sessions:") {
		t.Errorf("the failure was not reported: %q", errs.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, os.ErrClosed }

// configTargetPath is the discovery order the config subcommands follow.
func TestConfigTargetPathDiscoveryOrder(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	t.Chdir(dir)
	defer func() { _ = os.Chdir(cwd) }()

	t.Setenv("GOLM_CONFIG", "")
	explicit, err := configTargetPath("/explicit/path.json")
	if err != nil || explicit != "/explicit/path.json" {
		t.Errorf("explicit --config = %q, %v", explicit, err)
	}

	t.Setenv("GOLM_CONFIG", "/from/env.json")
	if p, _ := configTargetPath(""); p != "/from/env.json" {
		t.Errorf("GOLM_CONFIG ignored: %q", p)
	}

	if p, _ := configTargetPath("/explicit/path.json"); p != "/explicit/path.json" {
		t.Errorf("the environment overrode an explicit --config: %q", p)
	}

	t.Setenv("GOLM_CONFIG", "")
	if err := os.WriteFile(filepath.Join(dir, "golm.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if p, _ := configTargetPath(""); p != "golm.json" {
		t.Errorf("an existing ./golm.json was not preferred: %q", p)
	}
}

// withTimeout must compose rather than replace.
func TestWithTimeoutComposesBothCancels(t *testing.T) {
	var stopped bool
	stop := func() { stopped = true }

	bf := &backendFlags{}
	ctx, cancel := bf.withTimeout(context.Background(), stop)
	if _, ok := ctx.Deadline(); ok {
		t.Error("no timeout was set, yet the context carries a deadline")
	}
	cancel()
	if !stopped {
		t.Error("the caller's stop was not called")
	}

	stopped = false
	bf = &backendFlags{timeout: 50 * time.Millisecond}
	ctx, cancel = bf.withTimeout(context.Background(), stop)
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("a timeout was set but the context has no deadline")
	}
	cancel()
	if !stopped {
		t.Error("cancelling the composed func did not release the signal handler")
	}
}
