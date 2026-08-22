package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

type Store struct {
	db *sql.DB
	// quorum is how many distinct clients must confirm a verdict before it is
	// served; tests lower it to keep their fixtures readable.
	quorum int
}

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
	if _, err := db.Exec(schema + quarantineSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("server: schema: %w", err)
	}
	return &Store{db: db, quorum: defaultQuorum}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Bucket returns every published verdict whose hash starts with prefix. The
// prefix is all the server ever learns about what a client is reading. Rows in
// quarantine are withheld: an unconfirmed verdict is one stranger's claim, and
// serving it would let that stranger rewrite what every client sees.
func (s *Store) Bucket(prefix string, normVersion int) ([]core.BucketEntry, error) {
	rows, err := s.db.Query(
		`SELECT v.hash, v.verdict, v.reasons, v.source, v.votes FROM verdicts v
		 WHERE v.norm_version = ? AND v.prefix = ?
		   AND (SELECT COUNT(*) FROM confirmations c
		        WHERE c.hash = v.hash AND c.norm_version = v.norm_version) >= ?
		 ORDER BY v.hash`,
		normVersion, prefix, s.quorum)
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

// put writes one verdict as already published: it is the seeding path for the
// read tests, which need a bucket to read without staging a quorum of clients.
func (s *Store) put(e core.BucketEntry, normVersion int) error {
	if err := upsert(s.db, e, normVersion); err != nil {
		return err
	}
	for i := 0; i < s.quorum; i++ {
		if err := confirm(s.db, e.Hash, normVersion, "seed-"+strconv.Itoa(i), false); err != nil {
			return err
		}
	}
	return nil
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
func (s *Store) submit(e core.SubmitEntry, normVersion int, clientID string) (accepted bool, votes int, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, 0, fmt.Errorf("server: submit: %w", err)
	}
	defer tx.Rollback()

	row := core.BucketEntry{Hash: e.Hash, Verdict: e.Verdict, Reasons: e.Reasons, Source: e.Source, Votes: 1}

	var curVerdict, curSource, curReasons string
	var curVotes int
	changed := false
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
	default:
		changed = true
	}

	if err := upsert(tx, row, normVersion); err != nil {
		return false, 0, err
	}
	if err := confirm(tx, e.Hash, normVersion, clientID, changed); err != nil {
		return false, 0, err
	}
	if err := tx.Commit(); err != nil {
		return false, 0, fmt.Errorf("server: submit: %w", err)
	}
	return true, row.Votes, nil
}
