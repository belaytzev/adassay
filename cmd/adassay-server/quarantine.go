package main

import (
	"fmt"
	"time"
)

// defaultQuorum is how many distinct client UUIDs must agree before a verdict
// is served. Volume proves nothing: one client repeating itself, or flooding
// the batch endpoint with thousands of hashes, still publishes nothing.
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

// confirm records which verdict this client backs for hash. A client holds one
// opinion per hash: submitting a different verdict moves its confirmation
// rather than adding a second one, so repetition never counts as agreement.
//
// The move is refused when it comes from a weaker source than the one already
// on file. A client's outbox flush and its human vote are separate processes:
// a batch loaded before the vote can arrive after it, and without this guard
// that stale rules row would quietly retract the reader's correction.
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

// backers counts the distinct clients behind one verdict for hash. It is both
// the quarantine gate and the published vote count: anything derived from the
// number of requests instead would be a number one client can dial up alone.
func backers(ex queryer, hash string, normVersion int, verdict string) (int, error) {
	var n int
	err := ex.QueryRow(
		`SELECT COUNT(*) FROM confirmations WHERE hash = ? AND norm_version = ? AND verdict = ?`,
		hash, normVersion, verdict).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("server: quarantine: %w", err)
	}
	return n, nil
}
