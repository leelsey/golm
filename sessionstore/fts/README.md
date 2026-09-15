# sessionstore/fts

A `golm.SessionStore` backed by SQLite with FTS5: real full-text query syntax,
BM25 relevance ranking, and query time proportional to the answer rather than to
the store.

It is a **separate module**. golm's claim is that embedding it imposes no
dependency tree on the host program, so the one backend that needs a third-party
driver lives outside the main module and is paid for only by the deployments
that ask for it:

```sh
go get github.com/leelsey/golm/sessionstore/fts
```

```go
store, err := fts.Open("sessions.db")
if err != nil { … }
defer store.Close()
```

The driver is `modernc.org/sqlite` — pure Go, so this still cross-compiles
without cgo.

## When to use it over the default

`sessionstore.Files` keeps a small sidecar beside every session, which already
makes listing and searching cheap. Reach for this one when you want what an
index alone cannot give:

| | `Files` (default, zero-dep) | `fts` (this module) |
|---|---|---|
| Query syntax | every whitespace-separated term, substring | FTS5 `MATCH`: phrases, prefixes, `AND`/`OR`/`NOT`, `NEAR` |
| Ordering | newest first | **relevance** (BM25), then newest |
| Writes | one file per session, plus a sidecar | one transactional database |
| Dependencies | none | SQLite driver |

## Query syntax

`SessionQuery.Text` is an FTS5 `MATCH` expression, which is a different language
from the default store's. `security AND (audit OR review)` and `"exact phrase"`
and `wildcard*` all work here and none of them mean the same thing against
`Files`. A malformed expression is reported as such rather than returning
nothing.
