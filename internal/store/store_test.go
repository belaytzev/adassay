package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/belaytzev/adfilter/internal/core"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "nested", "verdicts.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestUpsertAndLookup(t *testing.T) {
	s := open(t)
	hash := Hash("Партнёрский материал")

	if _, ok, err := s.Lookup(hash); err != nil || ok {
		t.Fatalf("Lookup on empty db = %v, %v, want false, nil", ok, err)
	}

	rec := Record{Hash: hash, Verdict: core.Drop, Reasons: []string{"disclaimer"}, Source: core.SourceRules}
	if err := s.Upsert(rec); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, ok, err := s.Lookup(hash)
	if err != nil || !ok {
		t.Fatalf("Lookup = %v, %v, want true, nil", ok, err)
	}
	if got.Verdict != core.Drop || got.Source != core.SourceRules || len(got.Reasons) != 1 || got.Reasons[0] != "disclaimer" {
		t.Fatalf("Lookup = %+v, want the row that was written", got)
	}
	if got.Seen != 1 || got.Updated.IsZero() {
		t.Fatalf("Lookup = %+v, want seen 1 and a timestamp", got)
	}

	rec.Verdict = core.Flag
	rec.Source = core.SourceHuman
	rec.Votes = 3
	if err := s.Upsert(rec); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}
	got, _, err = s.Lookup(hash)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.Verdict != core.Flag || got.Source != core.SourceHuman || got.Votes != 3 || got.Seen != 2 {
		t.Fatalf("after re-upsert = %+v, want the new verdict and seen 2", got)
	}
}

func TestUpsertRejectsUnknownSource(t *testing.T) {
	s := open(t)
	if err := s.Upsert(Record{Hash: Hash("x"), Source: "guesswork"}); err == nil {
		t.Fatal("Upsert accepted an unknown source")
	}
}

func TestLookupIsScopedToNormVersion(t *testing.T) {
	s := open(t)
	hash := Hash("Партнёрский материал")
	if err := s.Upsert(Record{Hash: hash, Verdict: core.Drop, Source: core.SourceRules}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// A row written under another normalization version must not answer a
	// current-version lookup, and must not be overwritten by one either.
	if _, err := s.db.Exec(
		`INSERT INTO verdicts (hash, norm_version, verdict, reasons, source, votes, seen, updated)
		 VALUES (?, ?, 'keep', '', 'rules', 0, 1, ?)`,
		hash, core.NormVersion+1, time.Now().Unix()); err != nil {
		t.Fatalf("insert other version: %v", err)
	}

	got, ok, err := s.Lookup(hash)
	if err != nil || !ok {
		t.Fatalf("Lookup = %v, %v", ok, err)
	}
	if got.Verdict != core.Drop {
		t.Fatalf("Lookup crossed into another norm_version: %+v", got)
	}
}

func TestReopenKeepsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "verdicts.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Upsert(Record{Hash: Hash("promo"), Verdict: core.Drop, Source: core.SourceRules}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()
	if _, ok, err := s.Lookup(Hash("promo")); err != nil || !ok {
		t.Fatalf("after reopen Lookup = %v, %v, want true, nil", ok, err)
	}
}

// The timestamp dates the evidence, and L3 reads the score right after Visit:
// if a clean visit refreshed it, decay would always see an age of zero and
// half_life_days would be config with no effect.
func TestCleanVisitKeepsEvidenceAge(t *testing.T) {
	s := open(t)
	if err := s.Visit("promo.example", 2); err != nil {
		t.Fatalf("Visit: %v", err)
	}
	old := time.Now().AddDate(0, 0, -90).Unix()
	if _, err := s.db.Exec(`UPDATE domains SET updated_at = ?`, old); err != nil {
		t.Fatalf("age the row: %v", err)
	}

	if err := s.Visit("promo.example", 0); err != nil {
		t.Fatalf("Visit: %v", err)
	}
	got, err := s.Domain("promo.example")
	if err != nil {
		t.Fatalf("Domain: %v", err)
	}
	if got.UpdatedAt.Unix() != old {
		t.Errorf("UpdatedAt = %v, want the age of the last finding", got.UpdatedAt)
	}

	if err := s.Visit("promo.example", 1); err != nil {
		t.Fatalf("Visit: %v", err)
	}
	got, err = s.Domain("promo.example")
	if err != nil {
		t.Fatalf("Domain: %v", err)
	}
	if time.Since(got.UpdatedAt) > time.Minute {
		t.Errorf("UpdatedAt = %v, want now: a visit with findings dates itself", got.UpdatedAt)
	}
}

func TestVisitCounts(t *testing.T) {
	s := open(t)
	if got, err := s.Domain("habr.com"); err != nil || got.Visits != 0 {
		t.Fatalf("unknown domain = %+v, %v, want zero stats", got, err)
	}
	for _, findings := range []int{0, 2, 1} {
		if err := s.Visit("habr.com", findings); err != nil {
			t.Fatalf("Visit: %v", err)
		}
	}
	if err := s.Visit("example.com", 5); err != nil {
		t.Fatalf("Visit: %v", err)
	}

	got, err := s.Domain("habr.com")
	if err != nil {
		t.Fatalf("Domain: %v", err)
	}
	if got.Visits != 3 || got.Findings != 3 {
		t.Fatalf("Domain = %+v, want 3 visits and 3 findings", got)
	}
	if got.UpdatedAt.IsZero() {
		t.Fatal("Domain has no timestamp")
	}
}

func TestDefaultPathHonoursEnv(t *testing.T) {
	t.Setenv(EnvDB, "/tmp/adfilter-test.db")
	if got, err := DefaultPath(); err != nil || got != "/tmp/adfilter-test.db" {
		t.Fatalf("DefaultPath = %q, %v, want the env override", got, err)
	}

	t.Setenv(EnvDB, "")
	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if filepath.Base(got) != "verdicts.db" || !filepath.IsAbs(got) {
		t.Fatalf("DefaultPath = %q, want an absolute path to verdicts.db", got)
	}
}

func TestOutboxRoundTrip(t *testing.T) {
	s := open(t)
	entries := []core.SubmitEntry{
		{Hash: HexHash("first sponsored paragraph"), Verdict: core.Drop, Reasons: []string{"promo_code"}, Source: core.SourceRules},
		{Hash: HexHash("second sponsored paragraph"), Verdict: core.Drop, Source: core.SourceOllama},
	}
	for _, e := range entries {
		if err := s.Enqueue(e); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	// The same segment seen twice is one row: a duplicate would inflate the
	// batch and say more about this reader than about the segment.
	if err := s.Enqueue(entries[0]); err != nil {
		t.Fatalf("enqueue again: %v", err)
	}

	pending, oldest, err := s.Pending()
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("want 2 pending entries, got %d", len(pending))
	}
	if oldest.IsZero() {
		t.Error("oldest timestamp missing")
	}
	if len(pending[0].Reasons) != 1 || pending[0].Reasons[0] != "promo_code" {
		t.Errorf("reasons lost on the way to disk: %+v", pending[0])
	}

	if err := s.ClearPending([]string{entries[0].Hash}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	pending, _, err = s.Pending()
	if err != nil {
		t.Fatalf("pending after clear: %v", err)
	}
	if len(pending) != 1 || pending[0].Hash != entries[1].Hash {
		t.Errorf("clear removed the wrong rows: %+v", pending)
	}
}

func TestEnqueueRejectsUnknownSource(t *testing.T) {
	if err := open(t).Enqueue(core.SubmitEntry{Hash: HexHash("x"), Verdict: core.Drop, Source: "guesswork"}); err == nil {
		t.Error("want an error for an unknown source")
	}
}
