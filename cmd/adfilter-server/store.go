package main

import (
	"database/sql"
	"encoding/json"
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
	if !core.ValidSource(e.Source) {
		return fmt.Errorf("server: unknown source %q", e.Source)
	}
	_, err := s.db.Exec(
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
