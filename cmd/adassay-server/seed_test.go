package main

import (
	"strconv"

	"adassay.com/internal/core"
)

// put writes one verdict as already published. It exists for the read tests,
// which need a bucket to read without staging a quorum of clients — and it
// lives in a test file so no handler can reach a write path that skips
// quarantine.
func (s *Store) put(e core.BucketEntry, normVersion int) error {
	if err := upsert(s.db, e, normVersion); err != nil {
		return err
	}
	for i := 0; i < s.quorum; i++ {
		if err := confirm(s.db, e.Hash, normVersion, "seed-"+strconv.Itoa(i), e.Verdict.String(), e.Source); err != nil {
			return err
		}
	}
	return nil
}
