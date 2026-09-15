// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

// Package sessionstore provides golm.SessionStore backends.
package sessionstore

import (
	"sort"
	"strings"

	"github.com/leelsey/golm"
)

const snippetMax = 160

func metaOf(d golm.SessionData) golm.SessionMeta {
	return golm.SessionMeta{
		ID: d.ID, Parent: d.Parent, Title: d.Title,
		Created: d.Created, Updated: d.Updated, Messages: len(d.History),
	}
}

func selects(m golm.SessionMeta, q golm.SessionQuery) bool {
	if q.Parent != "" && m.Parent != q.Parent {
		return false
	}
	if !q.Since.IsZero() && m.Updated.Before(q.Since) {
		return false
	}
	if !q.Until.IsZero() && m.Updated.After(q.Until) {
		return false
	}
	return true
}

func byUpdated(a, b golm.SessionMeta) bool {
	if a.Updated.Equal(b.Updated) {
		return a.ID < b.ID
	}
	return a.Updated.After(b.Updated)
}

func sortMetas(ms []golm.SessionMeta) {
	sort.Slice(ms, func(i, j int) bool { return byUpdated(ms[i], ms[j]) })
}

func sortHits(hs []golm.SessionHit) {
	sort.Slice(hs, func(i, j int) bool { return byUpdated(hs[i].Meta, hs[j].Meta) })
}

func pageBounds(n int, q golm.SessionQuery) (int, int) {
	lo := q.Offset
	if lo < 0 {
		lo = 0
	}
	if lo > n {
		lo = n
	}
	hi := n
	if q.Limit > 0 && lo+q.Limit < hi {
		hi = lo + q.Limit
	}
	return lo, hi
}

func terms(text string) []string { return strings.Fields(strings.ToLower(text)) }

func containsAll(s string, terms []string) bool {
	for _, t := range terms {
		if !strings.Contains(s, t) {
			return false
		}
	}
	return true
}

func hitFor(d golm.SessionData, terms []string) (golm.SessionHit, bool) {
	m := metaOf(d)
	if len(terms) == 0 {
		return golm.SessionHit{Meta: m, Index: -1, Snippet: snippet(d.Title, "")}, true
	}
	if d.Title != "" && containsAll(strings.ToLower(d.Title), terms) {
		return golm.SessionHit{Meta: m, Index: -1, Snippet: snippet(d.Title, terms[0])}, true
	}
	for i, msg := range d.History {
		text := msg.Text()
		if text != "" && containsAll(strings.ToLower(text), terms) {
			return golm.SessionHit{Meta: m, Index: i, Snippet: snippet(text, terms[0])}, true
		}
	}
	return golm.SessionHit{}, false
}

func snippet(text, term string) string {
	text = strings.Join(strings.Fields(text), " ")
	r := []rune(text)
	if len(r) <= snippetMax {
		return text
	}
	at := 0
	if term != "" {
		if i := strings.Index(strings.ToLower(text), term); i > 0 {
			at = len([]rune(text[:i]))
		}
	}
	start := at - snippetMax/2
	if start < 0 {
		start = 0
	}
	end := start + snippetMax
	if end > len(r) {
		end, start = len(r), len(r)-snippetMax
	}
	out := string(r[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(r) {
		out += "…"
	}
	return out
}
