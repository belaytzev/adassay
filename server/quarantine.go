package server

import (
	"fmt"
	"strings"
	"time"
)

const defaultQuorum = 3

const quarantineSchema = `
CREATE TABLE IF NOT EXISTS confirmations (
	hash         TEXT    NOT NULL,
	norm_version INTEGER NOT NULL,
	client_id    TEXT    NOT NULL,
	verdict      TEXT    NOT NULL,
	source_rank  INTEGER NOT NULL DEFAULT 0,
	created      INTEGER NOT NULL,
	refuted      INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (hash, norm_version, client_id)
);
`

// Marks the confirmations selected by one column and charges each install
// once: a row already marked is skipped, so flipping back and forth cannot
// refute the same backing twice. The UPDATE locks the rows it marks, which
// keeps two concurrent overturns from charging the same install on postgres.
func refute(b binder, hash string, normVersion int, column, value string) error {
	rows, err := b.Query(
		`UPDATE confirmations SET refuted = 1
		 WHERE hash = ? AND norm_version = ? AND refuted = 0 AND `+column+` = ?
		 RETURNING client_id`,
		hash, normVersion, value)
	if err != nil {
		return fmt.Errorf("server: refute: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("server: refute: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("server: refute: %w", err)
	}
	rows.Close()
	if len(ids) == 0 {
		return nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	if _, err := b.Exec(
		`UPDATE installs SET refuted = refuted + 1 WHERE client_id IN (`+strings.Repeat("?,", len(ids)-1)+`?)`,
		args...); err != nil {
		return fmt.Errorf("server: refute: %w", err)
	}
	return nil
}

func confirm(ex execer, hash string, normVersion int, clientID, verdict, source string) error {
	_, err := ex.Exec(
		`INSERT INTO confirmations (hash, norm_version, client_id, verdict, source_rank, created)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (hash, norm_version, client_id) DO UPDATE SET
		     verdict = excluded.verdict,
		     source_rank = excluded.source_rank,
		     created = excluded.created
		 WHERE excluded.source_rank >= confirmations.source_rank`,
		hash, normVersion, clientID, verdict, sourceRank(source), time.Now().Unix())
	if err != nil {
		return fmt.Errorf("server: quarantine: %w", err)
	}
	return nil
}

// The predicate the read paths use to decide whether a verdict is served. It
// has to agree with backers below: one count decides what is accepted, the
// other what is served, and a verdict accepted but never served — or served
// on backing that was refused — is the defect that keeps coming back.
//
// Correlated on `v`, so the caller selects FROM verdicts v. Its placeholders
// are the two clamp bounds and the age cutoff, in that order.
func backersOf(clamp string) string {
	return `(SELECT COALESCE(SUM(` + clamp + `), 0)
	         FROM confirmations c JOIN installs i ON i.client_id = c.client_id
	         WHERE c.hash = v.hash AND c.norm_version = v.norm_version
	           AND c.verdict = v.verdict AND i.created <= ?)`
}

func backersCutoff() int64 { return time.Now().Add(-installMinAge).Unix() }

func backers(ex queryer, clamp, hash string, normVersion int, verdict string) (int, error) {
	cutoff := backersCutoff()
	var n int
	err := ex.QueryRow(
		`SELECT COALESCE(SUM(`+clamp+`), 0)
		 FROM confirmations c JOIN installs i ON i.client_id = c.client_id
		 WHERE c.hash = ? AND c.norm_version = ? AND c.verdict = ? AND i.created <= ?`,
		installMaxWeit, refutedPenalty, hash, normVersion, verdict, cutoff).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("server: quarantine: %w", err)
	}
	return n, nil
}
