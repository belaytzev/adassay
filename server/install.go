package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	installSchema = `
CREATE TABLE IF NOT EXISTS installs (
	client_id TEXT    PRIMARY KEY,
	secret    BLOB    NOT NULL,
	created   INTEGER NOT NULL,
	upheld    INTEGER NOT NULL DEFAULT 0,
	refuted   INTEGER NOT NULL DEFAULT 0,
	seeder    INTEGER NOT NULL DEFAULT 0
);
`
	installMinAge  = 24 * time.Hour
	installMaxWeit = 3
	refutedPenalty = 2
)

var errUnknownInstall = errors.New("server: unknown install")

type install struct {
	clientID string
	secret   []byte
	created  time.Time
	upheld   int
	refuted  int
	seeder   bool
}

func registerInstall(ex execer) (string, string, error) {
	id := uuid.NewString()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("server: register: %w", err)
	}
	sum := sha256.Sum256(raw)
	if _, err := ex.Exec(
		`INSERT INTO installs (client_id, secret, created) VALUES (?, ?, ?)`,
		id, sum[:], time.Now().Unix()); err != nil {
		return "", "", fmt.Errorf("server: register: %w", err)
	}
	return id, hex.EncodeToString(raw), nil
}

func loadInstall(q queryer, clientID string) (install, error) {
	var in install
	var created int64
	var seeder int
	err := q.QueryRow(
		`SELECT client_id, secret, created, upheld, refuted, seeder FROM installs WHERE client_id = ?`,
		clientID).Scan(&in.clientID, &in.secret, &created, &in.upheld, &in.refuted, &seeder)
	if err != nil {
		return install{}, errUnknownInstall
	}
	in.created = time.Unix(created, 0)
	in.seeder = seeder != 0
	return in, nil
}

func (in install) verify(presented string) bool {
	raw, err := hex.DecodeString(presented)
	if err != nil {
		return false
	}
	sum := sha256.Sum256(raw)
	return subtle.ConstantTimeCompare(sum[:], in.secret) == 1
}

func (in install) weight(now time.Time) int {
	if now.Sub(in.created) < installMinAge {
		return 0
	}
	w := 1 + min(in.upheld, installMaxWeit) - refutedPenalty*in.refuted
	return max(w, 0)
}

func (s *Store) markSeeders(ids []string) error {
	var wanted []string
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			wanted = append(wanted, id)
		}
	}

	if _, err := s.conn().Exec(`UPDATE installs SET seeder = 0 WHERE seeder = 1`); err != nil {
		return fmt.Errorf("server: clear seeders: %w", err)
	}
	for _, id := range wanted {
		res, err := s.conn().Exec(`UPDATE installs SET seeder = 1 WHERE client_id = ?`, id)
		if err != nil {
			return fmt.Errorf("server: mark seeder: %w", err)
		}
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			slog.Warn("server: no such install to mark as seeder", "client_id", id)
		}
	}
	return nil
}
