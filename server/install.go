package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
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
	refuted   INTEGER NOT NULL DEFAULT 0
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
	err := q.QueryRow(
		`SELECT client_id, secret, created, upheld, refuted FROM installs WHERE client_id = ?`,
		clientID).Scan(&in.clientID, &in.secret, &created, &in.upheld, &in.refuted)
	if err != nil {
		return install{}, errUnknownInstall
	}
	in.created = time.Unix(created, 0)
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
