package server

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"

	"adassay.com/internal/core"
)

func (s *Store) put(e core.BucketEntry, normVersion int) error {
	if err := upsert(s.db, e, normVersion, false); err != nil {
		return err
	}
	raw, err := hex.DecodeString(testSecretHex)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	// The read path weighs its backers, so the confirmations need installs that
	// exist and are old enough to carry any weight at all.
	for i := 0; i < s.quorum; i++ {
		id := "seed-" + strconv.Itoa(i)
		if _, err := s.conn().Exec(
			`INSERT INTO installs (client_id, secret, created, upheld, refuted) VALUES (?, ?, ?, 0, 0)
			 ON CONFLICT (client_id) DO NOTHING`,
			id, sum[:], time.Now().Add(-30*24*time.Hour).Unix()); err != nil {
			return err
		}
		if err := confirm(s.db, e.Hash, normVersion, id, e.Verdict.String(), e.Source); err != nil {
			return err
		}
	}
	return nil
}
