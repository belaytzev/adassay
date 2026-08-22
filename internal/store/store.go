package store

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

// EnvDB overrides the default database location; the --db flag overrides both.
const EnvDB = "ADFILTER_DB"

type Store struct{ db *sql.DB }

// Record is one row of the verdict cache. Reasons travel with the verdict:
// a cached Drop without its reason is unreviewable.
type Record struct {
	Hash    []byte
	Verdict core.Verdict
	Reasons []string
	Source  string
	Votes   int
	Seen    int
	Updated time.Time
}

type DomainStats struct {
	Visits    int
	Findings  int
	UpdatedAt time.Time
}

// DefaultPath is the cache directory, since the file is a rebuildable cache of
// verdicts; config lives elsewhere and is not written here.
func DefaultPath() (string, error) {
	if p := os.Getenv(EnvDB); p != "" {
		return p, nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		dir, err = os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("store: no cache or config directory: %w", err)
		}
	}
	return filepath.Join(dir, "adfilter", "verdicts.db"), nil
}

// Open creates the file and its directory if needed and migrates the schema.
func Open(path string) (*Store, error) {
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Lookup returns the cached verdict for a segment hash, or false when the
// bucket is empty for the current normalization version.
func (s *Store) Lookup(hash []byte) (Record, bool, error) {
	row := s.db.QueryRow(
		`SELECT verdict, reasons, source, votes, seen, updated FROM verdicts WHERE hash = ? AND norm_version = ?`,
		hash, core.NormVersion)

	var verdict, reasons, source string
	var votes, seen, updated int64
	switch err := row.Scan(&verdict, &reasons, &source, &votes, &seen, &updated); {
	case errors.Is(err, sql.ErrNoRows):
		return Record{}, false, nil
	case err != nil:
		return Record{}, false, fmt.Errorf("store: lookup: %w", err)
	}

	v, err := core.ParseVerdict(verdict)
	if err != nil {
		return Record{}, false, fmt.Errorf("store: lookup: %w", err)
	}
	rec := Record{
		Hash:    hash,
		Verdict: v,
		Reasons: decodeReasons(reasons),
		Source:  source,
		Votes:   int(votes),
		Seen:    int(seen),
		Updated: time.Unix(updated, 0).UTC(),
	}
	return rec, true, nil
}

// Upsert writes a verdict and counts the sighting. Which of two verdicts wins
// is decided by the pipeline before it gets here — this layer only stores.
func (s *Store) Upsert(rec Record) error {
	if !core.ValidSource(rec.Source) {
		return fmt.Errorf("store: unknown source %q", rec.Source)
	}
	updated := rec.Updated
	if updated.IsZero() {
		updated = time.Now()
	}
	_, err := s.db.Exec(
		`INSERT INTO verdicts (hash, norm_version, verdict, reasons, source, votes, seen, updated)
		 VALUES (?, ?, ?, ?, ?, ?, 1, ?)
		 ON CONFLICT (hash, norm_version) DO UPDATE SET
		 	verdict = excluded.verdict,
		 	reasons = excluded.reasons,
		 	source  = excluded.source,
		 	votes   = excluded.votes,
		 	seen    = verdicts.seen + 1,
		 	updated = excluded.updated`,
		rec.Hash, core.NormVersion, rec.Verdict.String(), encodeReasons(rec.Reasons),
		rec.Source, rec.Votes, updated.Unix())
	if err != nil {
		return fmt.Errorf("store: upsert: %w", err)
	}
	return nil
}

// Visit counts one page of a domain and the hidden-text findings it carried;
// L3 reads the ratio back through Domain.
func (s *Store) Visit(domain string, findings int) error {
	_, err := s.db.Exec(
		`INSERT INTO domains (hash, norm_version, visits, findings, updated_at)
		 VALUES (?, ?, 1, ?, ?)
		 ON CONFLICT (hash, norm_version) DO UPDATE SET
		 	visits     = domains.visits + 1,
		 	findings   = domains.findings + excluded.findings,
		 	updated_at = excluded.updated_at`,
		HashDomain(domain), core.NormVersion, findings, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("store: visit: %w", err)
	}
	return nil
}

func (s *Store) Domain(domain string) (DomainStats, error) {
	row := s.db.QueryRow(
		`SELECT visits, findings, updated_at FROM domains WHERE hash = ? AND norm_version = ?`,
		HashDomain(domain), core.NormVersion)

	var visits, findings, updated int64
	switch err := row.Scan(&visits, &findings, &updated); {
	case errors.Is(err, sql.ErrNoRows):
		return DomainStats{}, nil
	case err != nil:
		return DomainStats{}, fmt.Errorf("store: domain: %w", err)
	}
	return DomainStats{Visits: int(visits), Findings: int(findings), UpdatedAt: time.Unix(updated, 0).UTC()}, nil
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

// Enqueue spools a verdict for later submission to the shared database. The
// row lives on disk because a CLI run lasts seconds: holding a batch back "for
// a few hours" is only possible across processes.
func (s *Store) Enqueue(e core.SubmitEntry) error {
	if !core.ValidSource(e.Source) {
		return fmt.Errorf("store: unknown source %q", e.Source)
	}
	_, err := s.db.Exec(
		`INSERT INTO outbox (hash, norm_version, verdict, reasons, source, created)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (hash, norm_version) DO NOTHING`,
		e.Hash, core.NormVersion, e.Verdict.String(), encodeReasons(e.Reasons), e.Source, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("store: enqueue: %w", err)
	}
	return nil
}

// Pending returns everything waiting to be sent and the age of the oldest row,
// which is what decides whether a flush is due.
func (s *Store) Pending() ([]core.SubmitEntry, time.Time, error) {
	rows, err := s.db.Query(
		`SELECT hash, verdict, reasons, source, created FROM outbox WHERE norm_version = ? ORDER BY created`,
		core.NormVersion)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("store: pending: %w", err)
	}
	defer rows.Close()

	var entries []core.SubmitEntry
	var oldest time.Time
	for rows.Next() {
		var hash, verdict, reasons, source string
		var created int64
		if err := rows.Scan(&hash, &verdict, &reasons, &source, &created); err != nil {
			return nil, time.Time{}, fmt.Errorf("store: pending: %w", err)
		}
		v, err := core.ParseVerdict(verdict)
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("store: pending: %w", err)
		}
		if oldest.IsZero() {
			oldest = time.Unix(created, 0).UTC()
		}
		entries = append(entries, core.SubmitEntry{Hash: hash, Verdict: v, Reasons: decodeReasons(reasons), Source: source})
	}
	if err := rows.Err(); err != nil {
		return nil, time.Time{}, fmt.Errorf("store: pending: %w", err)
	}
	return entries, oldest, nil
}

// ClearPending drops the rows a flush managed to deliver; anything left keeps
// waiting for the next run.
func (s *Store) ClearPending(hashes []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: clear outbox: %w", err)
	}
	defer tx.Rollback()
	for _, h := range hashes {
		if _, err := tx.Exec(`DELETE FROM outbox WHERE hash = ? AND norm_version = ?`, h, core.NormVersion); err != nil {
			return fmt.Errorf("store: clear outbox: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: clear outbox: %w", err)
	}
	return nil
}
