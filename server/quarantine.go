package server

import (
	"fmt"
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
	PRIMARY KEY (hash, norm_version, client_id)
);
`

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

func backers(ex queryer, clamp, hash string, normVersion int, verdict string) (int, error) {
	cutoff := time.Now().Add(-installMinAge).Unix()
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
