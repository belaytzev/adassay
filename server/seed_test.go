package server

import (
	"strconv"

	"adassay.com/internal/core"
)

func (s *Store) put(e core.BucketEntry, normVersion int) error {
	if err := upsert(s.db, e, normVersion, false); err != nil {
		return err
	}
	for i := 0; i < s.quorum; i++ {
		if err := confirm(s.db, e.Hash, normVersion, "seed-"+strconv.Itoa(i), e.Verdict.String(), e.Source); err != nil {
			return err
		}
	}
	return nil
}
