package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/belaytzev/adfilter/internal/core"
	_ "modernc.org/sqlite"
)

// ponytail: sqlite+WAL, postgres if write contention shows up
const schema = `
CREATE TABLE IF NOT EXISTS verdicts (
	hash         TEXT    NOT NULL,
	norm_version INTEGER NOT NULL,
	prefix       TEXT    NOT NULL,
	verdict      TEXT    NOT NULL,
	reasons      TEXT    NOT NULL DEFAULT '',
	source       TEXT    NOT NULL,
	votes        INTEGER NOT NULL DEFAULT 0,
	updated      INTEGER NOT NULL,
	PRIMARY KEY (hash, norm_version)
);
CREATE INDEX IF NOT EXISTS verdicts_bucket ON verdicts (norm_version, prefix);
`

type Store struct{ db *sql.DB }

func openStore(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("server: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("server: open %s: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("server: schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Bucket returns every verdict whose hash starts with prefix. The prefix is all
// the server ever learns about what a client is reading.
func (s *Store) Bucket(prefix string, normVersion int) ([]core.BucketEntry, error) {
	rows, err := s.db.Query(
		`SELECT hash, verdict, reasons, source, votes FROM verdicts
		 WHERE norm_version = ? AND prefix = ? ORDER BY hash`,
		normVersion, prefix)
	if err != nil {
		return nil, fmt.Errorf("server: bucket: %w", err)
	}
	defer rows.Close()

	var entries []core.BucketEntry
	for rows.Next() {
		var hash, verdict, reasons, source string
		var votes int
		if err := rows.Scan(&hash, &verdict, &reasons, &source, &votes); err != nil {
			return nil, fmt.Errorf("server: bucket: %w", err)
		}
		v, err := core.ParseVerdict(verdict)
		if err != nil {
			return nil, fmt.Errorf("server: bucket: %w", err)
		}
		entries = append(entries, core.BucketEntry{
			Hash:    hash,
			Verdict: v,
			Reasons: decodeReasons(reasons),
			Source:  source,
			Votes:   votes,
		})
	}
	return entries, rows.Err()
}

// put writes one verdict. Task 18 puts an endpoint in front of it; the read
// tests need it to have something to read.
func (s *Store) put(e core.BucketEntry, normVersion int) error {
	return upsert(s.db, e, normVersion)
}

// execer is satisfied by both *sql.DB and *sql.Tx: submit needs its write to
// happen inside its own transaction, put does not care.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func upsert(ex execer, e core.BucketEntry, normVersion int) error {
	if !core.ValidSource(e.Source) {
		return fmt.Errorf("server: unknown source %q", e.Source)
	}
	_, err := ex.Exec(
		`INSERT INTO verdicts (hash, norm_version, prefix, verdict, reasons, source, votes, updated)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (hash, norm_version) DO UPDATE SET
		 	verdict = excluded.verdict,
		 	reasons = excluded.reasons,
		 	source  = excluded.source,
		 	votes   = excluded.votes,
		 	updated = excluded.updated`,
		e.Hash, normVersion, e.Hash[:core.PrefixLen], e.Verdict.String(),
		encodeReasons(e.Reasons), e.Source, e.Votes, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("server: put: %w", err)
	}
	return nil
}

func encodeReasons(reasons []string) string {
	if len(reasons) == 0 {
		return ""
	}
	b, err := json.Marshal(reasons)
	if err != nil {
		return ""
	}
	return string(b)
}

func decodeReasons(s string) []string {
	if s == "" {
		return nil
	}
	var reasons []string
	if err := json.Unmarshal([]byte(s), &reasons); err != nil {
		return nil
	}
	return reasons
}

// sourceRank orders verdict origins: a machine guess must never overturn a
// human, and a rules match must never overturn the judge.
func sourceRank(s string) int {
	switch s {
	case core.SourceRules:
		return 1
	case core.SourceOllama:
		return 2
	case core.SourceHuman:
		return 3
	}
	return 0
}

// submit merges one incoming verdict into the database and reports whether it
// was taken. Agreement adds a vote; disagreement is only allowed to rewrite the
// row when it comes from a stronger source.
// ponytail: first writer wins inside a tier, per-tier tallies if verdicts start flip-flopping
func (s *Store) submit(e core.SubmitEntry, normVersion int) (accepted bool, votes int, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, 0, fmt.Errorf("server: submit: %w", err)
	}
	defer tx.Rollback()

	row := core.BucketEntry{Hash: e.Hash, Verdict: e.Verdict, Reasons: e.Reasons, Source: e.Source, Votes: 1}

	var curVerdict, curSource, curReasons string
	var curVotes int
	switch err := tx.QueryRow(
		`SELECT verdict, source, reasons, votes FROM verdicts WHERE hash = ? AND norm_version = ?`,
		e.Hash, normVersion).Scan(&curVerdict, &curSource, &curReasons, &curVotes); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return false, 0, fmt.Errorf("server: submit: %w", err)
	case curVerdict == e.Verdict.String():
		row.Votes = curVotes + 1
		if sourceRank(e.Source) < sourceRank(curSource) {
			row.Source, row.Reasons = curSource, decodeReasons(curReasons)
		}
	case sourceRank(e.Source) <= sourceRank(curSource):
		return false, curVotes, nil
	}

	if err := upsert(tx, row, normVersion); err != nil {
		return false, 0, err
	}
	if err := tx.Commit(); err != nil {
		return false, 0, fmt.Errorf("server: submit: %w", err)
	}
	return true, row.Votes, nil
}
