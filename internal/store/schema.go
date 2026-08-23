package store

import (
	"database/sql"
	"fmt"

	"adassay.com/internal/core"
)

// ponytail: schema recreated per version, real migrations when the format churns
const schema = `
CREATE TABLE IF NOT EXISTS verdicts (
	hash         BLOB    NOT NULL,
	norm_version INTEGER NOT NULL,
	verdict      TEXT    NOT NULL,
	reasons      TEXT    NOT NULL DEFAULT '',
	source       TEXT    NOT NULL,
	votes        INTEGER NOT NULL DEFAULT 0,
	seen         INTEGER NOT NULL DEFAULT 1,
	updated      INTEGER NOT NULL,
	PRIMARY KEY (hash, norm_version)
);
CREATE TABLE IF NOT EXISTS outbox (
	hash         TEXT    NOT NULL,
	norm_version INTEGER NOT NULL,
	verdict      TEXT    NOT NULL,
	reasons      TEXT    NOT NULL DEFAULT '',
	source       TEXT    NOT NULL,
	created      INTEGER NOT NULL,
	PRIMARY KEY (hash, norm_version)
);
CREATE TABLE IF NOT EXISTS domains (
	hash         BLOB    NOT NULL,
	norm_version INTEGER NOT NULL,
	visits       INTEGER NOT NULL DEFAULT 0,
	findings     INTEGER NOT NULL DEFAULT 0,
	updated_at   INTEGER NOT NULL,
	PRIMARY KEY (hash, norm_version)
);
`

func migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("store: schema: %w", err)
	}
	// user_version records the normalization the file was written with; a bump
	// leaves the old rows in place but they are addressed by a different key.
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", core.NormVersion)); err != nil {
		return fmt.Errorf("store: user_version: %w", err)
	}
	return nil
}
