// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

// Package fts stores golm sessions in SQLite with FTS5.
package fts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/leelsey/golm"
	_ "modernc.org/sqlite"
)

// DefaultMaxBytes bounds one stored session, matching sessionstore.Files.
const DefaultMaxBytes int64 = 32 << 20

const snippetTokens = 24

// ErrBadQuery is a malformed FTS5 expression.
var ErrBadQuery = errors.New("sessionstore/fts: malformed search expression")

// Store is a SQLite-backed SessionStore.
type Store struct {
	db *sql.DB

	MaxBytes int64
}

var _ golm.SessionStore = (*Store)(nil)

// Open opens or creates the database at path.
func Open(path string) (*Store, error) {
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	if strings.HasPrefix(path, ":memory:") {
		dsn = path
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sessionstore/fts: open %s: %w", path, err)
	}

	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DB exposes the handle, for a caller that wants to back the store up, run PRAGMA optimize.
func (s *Store) DB() *sql.DB { return s.db }

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	id       TEXT PRIMARY KEY,
	parent   TEXT NOT NULL DEFAULT '',
	title    TEXT NOT NULL DEFAULT '',
	created  INTEGER NOT NULL,
	updated  INTEGER NOT NULL,
	messages INTEGER NOT NULL,
	data     BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_updated ON sessions(updated DESC);
CREATE INDEX IF NOT EXISTS sessions_parent  ON sessions(parent);

-- One row per message, so a hit knows WHICH message matched. SessionHit carries
-- that index, and a table indexed per session could only ever say "somewhere in
-- here". The title is row -1.
CREATE VIRTUAL TABLE IF NOT EXISTS msgindex USING fts5(
	sid UNINDEXED,
	idx UNINDEXED,
	body,
	tokenize = 'unicode61 remove_diacritics 2'
);
`

func (s *Store) migrate() error {
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("sessionstore/fts: schema: %w", err)
	}
	return nil
}

func (s *Store) limit() int64 {
	if s.MaxBytes > 0 {
		return s.MaxBytes
	}
	return DefaultMaxBytes
}

// Save writes the session and reindexes its messages, in one transaction.
func (s *Store) Save(ctx context.Context, sess *golm.Session) error {
	if sess == nil {
		return errors.New("sessionstore/fts: nil session")
	}
	d := sess.Snapshot()
	if d.ID == "" {
		return errors.New("sessionstore/fts: session has no id")
	}
	blob, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("sessionstore/fts: encode %s: %w", d.ID, err)
	}
	if int64(len(blob)) > s.limit() {
		return fmt.Errorf("sessionstore/fts: session %s is %d bytes, over MaxBytes %d", d.ID, len(blob), s.limit())
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sessions (id, parent, title, created, updated, messages, data)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			parent=excluded.parent, title=excluded.title, created=excluded.created,
			updated=excluded.updated, messages=excluded.messages, data=excluded.data`,
		d.ID, d.Parent, d.Title, unix(d.Created), unix(d.Updated), len(d.History), blob); err != nil {
		return fmt.Errorf("sessionstore/fts: save %s: %w", d.ID, err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM msgindex WHERE sid = ?`, d.ID); err != nil {
		return fmt.Errorf("sessionstore/fts: reindex %s: %w", d.ID, err)
	}
	ins, err := tx.PrepareContext(ctx, `INSERT INTO msgindex (sid, idx, body) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer ins.Close()
	if d.Title != "" {
		if _, err := ins.ExecContext(ctx, d.ID, -1, d.Title); err != nil {
			return err
		}
	}
	for i, m := range d.History {
		text := m.Text()
		if strings.TrimSpace(text) == "" {
			continue
		}
		if _, err := ins.ExecContext(ctx, d.ID, i, text); err != nil {
			return fmt.Errorf("sessionstore/fts: index %s message %d: %w", d.ID, i, err)
		}
	}
	return tx.Commit()
}

// Load rebuilds the session stored under id.
func (s *Store) Load(ctx context.Context, id string) (*golm.Session, error) {
	var blob []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM sessions WHERE id = ?`, id).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, golm.ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	if int64(len(blob)) > s.limit() {
		return nil, fmt.Errorf("sessionstore/fts: session %s is %d bytes, over MaxBytes %d", id, len(blob), s.limit())
	}
	var d golm.SessionData
	if err := json.Unmarshal(blob, &d); err != nil {
		return nil, fmt.Errorf("sessionstore/fts: decode %s: %w", id, err)
	}

	if d.ID != id {
		return nil, fmt.Errorf("sessionstore/fts: row %q holds id %q", id, d.ID)
	}
	return d.Session(), nil
}

// Delete removes the session and its indexed messages.
func (s *Store) Delete(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return golm.ErrSessionNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM msgindex WHERE sid = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// List returns matching sessions, newest first.
func (s *Store) List(ctx context.Context, q golm.SessionQuery) ([]golm.SessionMeta, error) {
	where, args := filterSQL(q, "")
	sqlText := `SELECT id, parent, title, created, updated, messages FROM sessions` +
		where + ` ORDER BY updated DESC, id ASC` + pageSQL(q)
	rows, err := s.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []golm.SessionMeta
	for rows.Next() {
		m, err := scanMeta(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Search returns matching sessions by RELEVANCE, one hit each.
func (s *Store) Search(ctx context.Context, q golm.SessionQuery) ([]golm.SessionHit, error) {
	if strings.TrimSpace(q.Text) == "" {
		metas, err := s.List(ctx, q)
		if err != nil {
			return nil, err
		}
		out := make([]golm.SessionHit, 0, len(metas))
		for _, m := range metas {
			out = append(out, golm.SessionHit{Meta: m, Index: -1, Snippet: m.Title})
		}
		return out, nil
	}

	where, args := filterSQL(q, "s.")

	sqlText := `
		SELECT s.id, s.parent, s.title, s.created, s.updated, s.messages,
		       msgindex.idx, snippet(msgindex, 2, '', '', '…', ?), bm25(msgindex) AS rank
		FROM msgindex
		JOIN sessions s ON s.id = msgindex.sid
		WHERE msgindex MATCH ?` + strings.TrimPrefix(where, " WHERE") + `
		ORDER BY rank ASC, s.updated DESC, s.id ASC`
	if where != "" {
		sqlText = `
		SELECT s.id, s.parent, s.title, s.created, s.updated, s.messages,
		       msgindex.idx, snippet(msgindex, 2, '', '', '…', ?), bm25(msgindex) AS rank
		FROM msgindex
		JOIN sessions s ON s.id = msgindex.sid
		WHERE msgindex MATCH ? AND` + strings.TrimPrefix(where, " WHERE") + `
		ORDER BY rank ASC, s.updated DESC, s.id ASC`
	}

	all := append([]any{snippetTokens, q.Text}, args...)
	rows, err := s.db.QueryContext(ctx, sqlText, all...)
	if err != nil {
		if isQuerySyntaxError(err) {
			return nil, fmt.Errorf("%w: %q: %v", ErrBadQuery, q.Text, err)
		}
		return nil, err
	}
	defer rows.Close()

	var out []golm.SessionHit
	seen := make(map[string]bool)
	for rows.Next() {
		var (
			m                golm.SessionMeta
			created, updated int64
			idx              int
			snip             string
			rank             float64
		)
		if err := rows.Scan(&m.ID, &m.Parent, &m.Title, &created, &updated, &m.Messages,
			&idx, &snip, &rank); err != nil {
			return nil, err
		}
		if seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		m.Created, m.Updated = fromUnix(created), fromUnix(updated)
		out = append(out, golm.SessionHit{Meta: m, Index: idx, Snippet: strings.TrimSpace(snip)})
	}
	if err := rows.Err(); err != nil {
		if isQuerySyntaxError(err) {
			return nil, fmt.Errorf("%w: %q: %v", ErrBadQuery, q.Text, err)
		}
		return nil, err
	}
	lo, hi := pageBounds(len(out), q)
	return out[lo:hi], nil
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

func filterSQL(q golm.SessionQuery, prefix string) (string, []any) {
	var clauses []string
	var args []any
	if q.Parent != "" {
		clauses = append(clauses, " "+prefix+"parent = ?")
		args = append(args, q.Parent)
	}
	if !q.Since.IsZero() {
		clauses = append(clauses, " "+prefix+"updated >= ?")
		args = append(args, unix(q.Since))
	}
	if !q.Until.IsZero() {
		clauses = append(clauses, " "+prefix+"updated <= ?")
		args = append(args, unix(q.Until))
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE" + strings.Join(clauses, " AND"), args
}

func pageSQL(q golm.SessionQuery) string {
	switch {
	case q.Limit > 0 && q.Offset > 0:
		return fmt.Sprintf(" LIMIT %d OFFSET %d", q.Limit, q.Offset)
	case q.Limit > 0:
		return fmt.Sprintf(" LIMIT %d", q.Limit)
	case q.Offset > 0:

		return fmt.Sprintf(" LIMIT -1 OFFSET %d", q.Offset)
	}
	return ""
}

func scanMeta(rows *sql.Rows) (golm.SessionMeta, error) {
	var (
		m                golm.SessionMeta
		created, updated int64
	)
	if err := rows.Scan(&m.ID, &m.Parent, &m.Title, &created, &updated, &m.Messages); err != nil {
		return golm.SessionMeta{}, err
	}
	m.Created, m.Updated = fromUnix(created), fromUnix(updated)
	return m, nil
}

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

func fromUnix(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}

var ftsParserErrors = []string{
	"fts5: syntax error",
	"unterminated string",
	"malformed match",
	"fts5: phrase",
	"unknown special query",
	"no such cursor",
	"expected phrase",
}

func isQuerySyntaxError(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, want := range ftsParserErrors {
		if strings.Contains(msg, want) {
			return true
		}
	}

	return strings.Contains(msg, "no such column")
}
