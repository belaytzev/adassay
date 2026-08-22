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
	created      INTEGER NOT NULL,
	PRIMARY KEY (hash, norm_version, client_id)
);
`

// confirm records one client's agreement with the verdict now stored for hash.
// When the verdict itself changed, the confirmations of the verdict it replaced
// are dropped: agreement with a verdict that no longer exists must not release
// its successor from quarantine.
func confirm(ex execer, hash string, normVersion int, clientID string, changed bool) error {
	if changed {
		if _, err := ex.Exec(`DELETE FROM confirmations WHERE hash = ? AND norm_version = ?`,
			hash, normVersion); err != nil {
			return fmt.Errorf("server: quarantine: %w", err)
		}
	}
	if _, err := ex.Exec(
		`INSERT OR IGNORE INTO confirmations (hash, norm_version, client_id, created) VALUES (?, ?, ?, ?)`,
		hash, normVersion, clientID, time.Now().Unix()); err != nil {
		return fmt.Errorf("server: quarantine: %w", err)
	}
	return nil
}
