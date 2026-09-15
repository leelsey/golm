// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/sessionstore"
)

func defaultSessionsDir() (string, error) {
	if d := os.Getenv("GOLM_SESSIONS"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "golm", "sessions"), nil
}

func openStore(dir string, wanted bool) (golm.SessionStore, error) {
	if dir == "" {
		if !wanted {
			return nil, nil
		}
		d, err := defaultSessionsDir()
		if err != nil {
			return nil, err
		}
		dir = d
	}
	return sessionstore.NewFiles(dir), nil
}

func resumeSession(ctx context.Context, store golm.SessionStore, id string) (*golm.Session, bool, error) {
	if store == nil || id == "" {
		return golm.NewSession(), false, nil
	}
	s, err := store.Load(ctx, id)
	switch {
	case err == nil:
		return s, true, nil
	case errors.Is(err, golm.ErrSessionNotFound):
		fresh := golm.SessionData{ID: id, Created: time.Now()}.Session()

		if err := store.Save(ctx, fresh); err != nil {
			return nil, false, err
		}
		return fresh, false, nil
	default:
		return nil, false, err
	}
}

func saveSession(ctx context.Context, store golm.SessionStore, s *golm.Session, stderr io.Writer) {
	if store == nil || s == nil {
		return
	}

	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := store.Save(sctx, s); err != nil {
		fmt.Fprintf(stderr, "golm: warning: session %s not saved: %v\n", s.ID(), err)
	}
}

func attachArchive(agent *golm.Agent, store golm.SessionStore) {
	if agent == nil || agent.Compaction == nil || store == nil {
		return
	}
	agent.Compaction.Archive = func(ctx context.Context, archived *golm.Session) error {
		return store.Save(ctx, archived)
	}
}

func runSessions(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		sessionsUsage(stderr)
		return 2
	}
	switch args[0] {
	case "list", "ls":
		return sessionsList(args[1:], stdout, stderr)
	case "show", "cat":
		return sessionsShow(args[1:], stdout, stderr)
	case "search":
		return sessionsSearch(args[1:], stdout, stderr)
	case "lineage":
		return sessionsLineage(args[1:], stdout, stderr)
	case "rm", "delete":
		return sessionsRemove(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		sessionsUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "golm sessions: unknown command %q\n", args[0])
		sessionsUsage(stderr)
		return 2
	}
}

func sessionsUsage(w io.Writer) {
	fmt.Fprint(w, `golm sessions — stored conversations

Usage (flags come before the id or text, as everywhere else here):
  golm sessions list [--store dir] [--limit n] [--parent id]
  golm sessions show [--store dir] <id>
  golm sessions search [--store dir] [--limit n] <text>
  golm sessions lineage [--store dir] <id>
  golm sessions rm [--store dir] <id>...

Sessions live in $GOLM_SESSIONS, or ~/.local/share/golm/sessions.
Run one with: golm --session <id> "prompt"   /   golm tui --session <id>
`)
}

func sessionFlags(name string, args []string, stderr io.Writer, extra func(*flag.FlagSet)) (golm.SessionStore, []string, int) {
	fs := flag.NewFlagSet("golm sessions "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("store", "", "directory holding the sessions")
	if extra != nil {
		extra(fs)
	}
	if code := parseScoped(fs, args, stderr, "sessions"); code >= 0 {
		return nil, nil, code
	}
	store, err := openStore(*dir, true)
	if err != nil {
		fmt.Fprintln(stderr, "golm sessions:", err)
		return nil, nil, 1
	}
	return store, fs.Args(), -1
}

func sessionsList(args []string, stdout, stderr io.Writer) int {
	var limit int
	var parent string
	store, _, code := sessionFlags("list", args, stderr, func(fs *flag.FlagSet) {
		fs.IntVar(&limit, "limit", 50, "maximum sessions to list")
		fs.StringVar(&parent, "parent", "", "only sessions compacted from this one")
	})
	if code >= 0 {
		return code
	}
	metas, err := store.List(context.Background(), golm.SessionQuery{Limit: limit, Parent: parent})
	if err != nil {
		fmt.Fprintln(stderr, "golm sessions:", err)
		return 1
	}
	if len(metas) == 0 {
		fmt.Fprintln(stdout, "no sessions")
		return 0
	}
	tw := tabwriter.NewWriter(stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tUPDATED\tMSGS\tTITLE")
	for _, m := range metas {
		title := m.Title
		if title == "" && m.Parent != "" {
			title = "(compacted from " + short(m.Parent) + ")"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", m.ID, m.Updated.Format(time.RFC3339), m.Messages, title)
	}
	return flushOr(tw, stderr)
}

func sessionsShow(args []string, stdout, stderr io.Writer) int {
	store, rest, code := sessionFlags("show", args, stderr, nil)
	if code >= 0 {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "golm sessions show: one session id required")
		return 2
	}
	s, err := store.Load(context.Background(), rest[0])
	if err != nil {
		fmt.Fprintln(stderr, "golm sessions:", err)
		return 1
	}
	m := s.Meta()
	fmt.Fprintf(stdout, "id      %s\n", m.ID)
	if m.Parent != "" {
		fmt.Fprintf(stdout, "parent  %s\n", m.Parent)
	}
	if m.Title != "" {
		fmt.Fprintf(stdout, "title   %s\n", m.Title)
	}
	fmt.Fprintf(stdout, "updated %s\nusage   %s\n\n", m.Updated.Format(time.RFC3339), s.Usage())
	for _, msg := range s.History() {
		fmt.Fprintf(stdout, "%s: %s\n", msg.Role, msg.Text())
	}
	return 0
}

func sessionsSearch(args []string, stdout, stderr io.Writer) int {
	var limit int
	store, rest, code := sessionFlags("search", args, stderr, func(fs *flag.FlagSet) {
		fs.IntVar(&limit, "limit", 20, "maximum hits to list")
	})
	if code >= 0 {
		return code
	}
	text := strings.Join(rest, " ")
	if text == "" {
		fmt.Fprintln(stderr, "golm sessions search: nothing to search for")
		return 2
	}
	hits, err := store.Search(context.Background(), golm.SessionQuery{Text: text, Limit: limit})
	if err != nil {
		fmt.Fprintln(stderr, "golm sessions:", err)
		return 1
	}
	if len(hits) == 0 {
		fmt.Fprintln(stdout, "no matches")
		return 0
	}
	for _, h := range hits {
		where := fmt.Sprintf("message %d", h.Index)
		if h.Index < 0 {
			where = "title"
		}
		fmt.Fprintf(stdout, "%s  %s\n    %s\n", h.Meta.ID, where, h.Snippet)
	}
	return 0
}

func sessionsLineage(args []string, stdout, stderr io.Writer) int {
	store, rest, code := sessionFlags("lineage", args, stderr, nil)
	if code >= 0 {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "golm sessions lineage: one session id required")
		return 2
	}
	chain, err := golm.Lineage(context.Background(), store, rest[0])
	if err != nil {
		fmt.Fprintln(stderr, "golm sessions:", err)
		return 1
	}

	for i, m := range chain {
		fmt.Fprintf(stdout, "%s%s  %d msgs  %s\n", strings.Repeat("  ", i), m.ID, m.Messages, m.Updated.Format(time.RFC3339))
	}
	return 0
}

func sessionsRemove(args []string, stdout, stderr io.Writer) int {
	store, rest, code := sessionFlags("rm", args, stderr, nil)
	if code >= 0 {
		return code
	}
	if len(rest) == 0 {
		fmt.Fprintln(stderr, "golm sessions rm: at least one session id required")
		return 2
	}
	status := 0
	for _, id := range rest {
		if err := store.Delete(context.Background(), id); err != nil {
			fmt.Fprintln(stderr, "golm sessions:", err)
			status = 1
			continue
		}
		fmt.Fprintln(stdout, "removed", id)
	}
	return status
}

func flushOr(tw *tabwriter.Writer, stderr io.Writer) int {
	if err := tw.Flush(); err != nil {
		fmt.Fprintln(stderr, "golm sessions:", err)
		return 1
	}
	return 0
}

func short(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
