package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"adassay.com/internal/core"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS verdicts (
	hash         TEXT    NOT NULL,
	norm_version INTEGER NOT NULL,
	prefix       TEXT    NOT NULL,
	verdict      TEXT    NOT NULL,
	reasons      TEXT    NOT NULL DEFAULT '',
	source       TEXT    NOT NULL,
	votes        INTEGER NOT NULL DEFAULT 0,
	published    INTEGER NOT NULL DEFAULT 0,
	updated      INTEGER NOT NULL,
	PRIMARY KEY (hash, norm_version)
);
CREATE INDEX IF NOT EXISTS verdicts_bucket ON verdicts (norm_version, prefix);
`

const statsTTL = 30 * time.Second

const maxBucket = 1024

type Store struct {
	db *sql.DB
	d  dialect

	quorum int
	mx     *metrics

	statsMu    sync.Mutex
	statsAt    time.Time
	statsCount [2]int
}

func openStore(dsn string) (*Store, error) {
	d := sqliteDialect
	driver, target := "sqlite", dsn+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate"

	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		d, driver, target = postgresDialect, "pgx", dsn
	} else if dir := filepath.Dir(dsn); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("server: %w", err)
		}
	}

	db, err := sql.Open(driver, target)
	if err != nil {
		return nil, fmt.Errorf("server: open %s: %w", d.name, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("server: connect %s: %w", d.name, err)
	}
	if _, err := db.Exec(d.schema()); err != nil {
		db.Close()
		return nil, fmt.Errorf("server: schema: %w", err)
	}

	st := &Store{db: db, d: d, quorum: defaultQuorum}

	var hasRank int
	if err := st.conn().QueryRow(d.hasSourceRank()).Scan(&hasRank); err != nil {
		db.Close()
		return nil, fmt.Errorf("server: inspect confirmations: %w", err)
	}
	if hasRank == 0 {
		if err := migrateSourceRank(db, d); err != nil {
			db.Close()
			return nil, fmt.Errorf("server: migrate source_rank: %w", err)
		}
	}

	var hasPublished int
	if err := st.conn().QueryRow(d.hasColumn("verdicts", "published")).Scan(&hasPublished); err != nil {
		db.Close()
		return nil, fmt.Errorf("server: inspect verdicts: %w", err)
	}
	if hasPublished == 0 {
		if _, err := db.Exec(`ALTER TABLE verdicts ADD COLUMN published INTEGER NOT NULL DEFAULT 0`); err != nil {
			db.Close()
			return nil, fmt.Errorf("server: migrate published: %w", err)
		}
		if _, err := db.Exec(`UPDATE verdicts SET published = 1 WHERE source = 'seed'`); err != nil {
			db.Close()
			return nil, fmt.Errorf("server: backfill published: %w", err)
		}
	}

	var hasSeeder int
	if err := st.conn().QueryRow(d.hasColumn("installs", "seeder")).Scan(&hasSeeder); err != nil {
		db.Close()
		return nil, fmt.Errorf("server: inspect installs: %w", err)
	}
	if hasSeeder == 0 {
		if _, err := db.Exec(`ALTER TABLE installs ADD COLUMN seeder INTEGER NOT NULL DEFAULT 0`); err != nil {
			db.Close()
			return nil, fmt.Errorf("server: migrate seeder: %w", err)
		}
	}
	return st, nil
}

func (s *Store) conn() binder { return binder{inner: s.db, d: s.d} }

func (s *Store) tx(t *sql.Tx) binder { return binder{inner: t, d: s.d} }

func migrateSourceRank(db *sql.DB, d dialect) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`ALTER TABLE confirmations ADD COLUMN source_rank INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}

	if _, err := tx.Exec(d.rebind(`UPDATE confirmations SET source_rank = ?`), sourceRank(core.SourceHuman)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) stats() (published, quarantined int, err error) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	if time.Since(s.statsAt) < statsTTL {
		return s.statsCount[0], s.statsCount[1], nil
	}
	published, quarantined, err = s.countVerdicts()
	if err != nil {
		return 0, 0, err
	}
	s.statsAt, s.statsCount = time.Now(), [2]int{published, quarantined}
	return published, quarantined, nil
}

func (s *Store) countVerdicts() (published, quarantined int, err error) {
	var total int
	err = s.conn().QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(CASE WHEN v.published = 1 OR
			(SELECT COUNT(*) FROM confirmations c
			 WHERE c.hash = v.hash AND c.norm_version = v.norm_version
			   AND c.verdict = v.verdict) >= ? THEN 1 ELSE 0 END), 0)
		 FROM verdicts v`, s.quorum).Scan(&total, &published)
	if err != nil {
		return 0, 0, fmt.Errorf("server: stats: %w", err)
	}
	return published, total - published, nil
}

func (s *Store) Bucket(prefix string, normVersion int) ([]core.BucketEntry, error) {
	rows, err := s.conn().Query(
		`SELECT v.hash, v.verdict, v.reasons, v.source, v.votes FROM verdicts v
		 WHERE v.norm_version = ? AND v.prefix = ?
		   AND (v.published = 1
		        OR (SELECT COUNT(*) FROM confirmations c
		            WHERE c.hash = v.hash AND c.norm_version = v.norm_version
		              AND c.verdict = v.verdict) >= ?)
		 ORDER BY v.hash
		 LIMIT ?`,
		normVersion, prefix, s.quorum, maxBucket)
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

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

type queryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

func upsert(ex execer, e core.BucketEntry, normVersion int, published bool) error {
	if !core.ValidSource(e.Source) {
		return fmt.Errorf("server: unknown source %q", e.Source)
	}
	flag := 0
	if published {
		flag = 1
	}
	_, err := ex.Exec(
		`INSERT INTO verdicts (hash, norm_version, prefix, verdict, reasons, source, votes, published, updated)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (hash, norm_version) DO UPDATE SET
		 	verdict   = excluded.verdict,
		 	reasons   = excluded.reasons,
		 	source    = excluded.source,
		 	votes     = excluded.votes,
		 	published = excluded.published,
		 	updated   = excluded.updated`,
		e.Hash, normVersion, e.Hash[:core.PrefixLen], e.Verdict.String(),
		encodeReasons(e.Reasons), e.Source, e.Votes, flag, time.Now().Unix())
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

func sourceRank(s string) int {
	switch s {
	case core.SourceRules:
		return 1
	case core.SourceOllama:
		return 2
	case core.SourceSeed:
		return 2
	case core.SourceHuman:
		return 3
	}
	return 0
}

func (s *Store) submit(e core.SubmitEntry, normVersion int, clientID string, seeder bool) (accepted bool, votes int, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, 0, fmt.Errorf("server: submit: %w", err)
	}
	defer tx.Rollback()

	row := core.BucketEntry{Hash: e.Hash, Verdict: e.Verdict, Reasons: e.Reasons, Source: e.Source}

	var curVerdict, curSource, curReasons string
	var curVotes, curPublished int
	challenger, refused := false, false

	// Publication is its own fact, not a shade of source: a row can be served
	// because a seeder vouched for it while still recording who decided it.
	publish := false
	switch err := s.tx(tx).QueryRow(
		`SELECT verdict, source, reasons, votes, published FROM verdicts WHERE hash = ? AND norm_version = ?`,
		e.Hash, normVersion).Scan(&curVerdict, &curSource, &curReasons, &curVotes, &curPublished); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return false, 0, fmt.Errorf("server: submit: %w", err)

	case curVerdict == e.Verdict.String():
		row.Source, row.Reasons = curSource, decodeReasons(curReasons)
		publish = curPublished == 1

	case sourceRank(e.Source) < sourceRank(curSource) && curVotes >= s.quorum:
		s.mx.diverged()
		refused = true
	default:
		// The verdict itself changed, so an earlier vouch does not carry.
		s.mx.diverged()
		challenger = true
	}

	btx := s.tx(tx)
	if err := confirm(btx, e.Hash, normVersion, clientID, e.Verdict.String(), e.Source); err != nil {
		return false, 0, err
	}
	if refused {
		if err := tx.Commit(); err != nil {
			return false, 0, fmt.Errorf("server: submit: %w", err)
		}
		return false, curVotes, nil
	}
	n, err := backers(btx, s.d.clampSum, e.Hash, normVersion, e.Verdict.String())
	if err != nil {
		return false, 0, err
	}

	reach := n
	if seeder && e.Source == core.SourceSeed {
		publish = true
		if reach < s.quorum {
			reach = s.quorum
		}
	}

	if challenger && reach < s.quorum {
		if err := tx.Commit(); err != nil {
			return false, 0, fmt.Errorf("server: submit: %w", err)
		}
		return true, curVotes, nil
	}
	row.Votes = n
	if err := upsert(btx, row, normVersion, publish); err != nil {
		return false, 0, err
	}
	if err := tx.Commit(); err != nil {
		return false, 0, fmt.Errorf("server: submit: %w", err)
	}
	return true, row.Votes, nil
}
