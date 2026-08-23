package pipeline

import (
	"bytes"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"adassay.com/internal/config"
	"adassay.com/internal/core"
	"adassay.com/internal/store"
)

const adText = "Sponsored post: our favourite grinder of the year, on sale this week."

var adLinks = []core.Link{{Href: "https://awin1.com/cread.php?p=42", Text: "buy it"}}

const plainText = "A burr grinder gives an even particle size, which matters more than the brewer."

type fakeCache struct {
	recs    map[string]store.Record
	lookups int
	upserts []store.Record
	score   float64
	err     error
}

func newCache() *fakeCache {
	return &fakeCache{recs: map[string]store.Record{}, score: 1}
}

func (c *fakeCache) put(text string, rec store.Record) {
	rec.Hash = store.Hash(text)
	c.recs[hex.EncodeToString(rec.Hash)] = rec
}

func (c *fakeCache) Lookup(hash []byte) (store.Record, bool, error) {
	c.lookups++
	if c.err != nil {
		return store.Record{}, false, c.err
	}
	rec, ok := c.recs[hex.EncodeToString(hash)]
	return rec, ok, nil
}

func (c *fakeCache) Upsert(rec store.Record) error {
	c.upserts = append(c.upserts, rec)
	return nil
}

func (c *fakeCache) Source(string, config.L3) (float64, error) { return c.score, c.err }

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return cfg
}

func run(t *testing.T, p *Pipeline, segs ...core.Segment) core.Result {
	t.Helper()
	res, err := p.Run(core.Result{Segments: segs, Domain: "example.com"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return res
}

func TestCacheHitSkipsRules(t *testing.T) {
	cache := newCache()
	cache.put(adText, store.Record{
		Verdict: core.Keep,
		Reasons: []string{"reviewed by hand"},
		Source:  core.SourceHuman,
	})
	p := &Pipeline{Cfg: testConfig(t), Cache: cache, Log: quiet()}

	res := run(t, p, core.Segment{ID: "s1", Text: adText, Links: adLinks})

	if got := res.Segments[0].Verdict; got != core.Keep {
		t.Errorf("verdict = %v, want keep — rules ran over a human verdict", got)
	}
	if got := res.Segments[0].Reasons; len(got) != 1 || got[0] != "reviewed by hand" {
		t.Errorf("reasons = %v, want the cached ones", got)
	}
	if len(cache.upserts) != 0 {
		t.Errorf("a cache hit must not write back: %v", cache.upserts)
	}
}

func TestVerdictPriority(t *testing.T) {
	order := []string{core.SourceRules, core.SourceOllama, core.SourceShared, core.SourceHuman}
	for i, lower := range order {
		for _, higher := range order[i+1:] {
			if !Outranks(higher, lower) {
				t.Errorf("Outranks(%q, %q) = false, want true", higher, lower)
			}
			if Outranks(lower, higher) {
				t.Errorf("Outranks(%q, %q) = true, want false", lower, higher)
			}
		}
		if Outranks(lower, lower) {
			t.Errorf("Outranks(%q, %q) = true, want false", lower, lower)
		}
	}

	for _, src := range []string{core.SourceOllama, core.SourceShared, core.SourceHuman} {
		cache := newCache()
		cache.put(adText, store.Record{Verdict: core.Keep, Source: src})
		p := &Pipeline{Cfg: testConfig(t), Cache: cache, Log: quiet()}

		res := run(t, p, core.Segment{ID: "s1", Text: adText, Links: adLinks})
		if got := res.Segments[0].Verdict; got != core.Keep {
			t.Errorf("source %q: verdict = %v, want keep", src, got)
		}
	}

	cache := newCache()
	cache.put(adText, store.Record{Verdict: core.Keep, Source: core.SourceRules})
	p := &Pipeline{Cfg: testConfig(t), Cache: cache, Log: quiet()}

	res := run(t, p, core.Segment{ID: "s1", Text: adText, Links: adLinks})
	if got := res.Segments[0].Verdict; got != core.Drop {
		t.Errorf("source rules: verdict = %v, want drop from a re-run", got)
	}
}

func TestDomainNeverDropsAlone(t *testing.T) {
	cfg := testConfig(t)

	cfg.L2.Hi, cfg.L2.Lo = 0.35, 0.3
	cfg.L3.MaxShift = 1

	cache := newCache()
	cache.score = 0
	p := &Pipeline{Cfg: cfg, Cache: cache, Log: quiet()}

	res := run(t, p, core.Segment{ID: "s1", Text: plainText, Links: adLinks})
	seg := res.Segments[0]

	if seg.Verdict != core.Flag {
		t.Fatalf("verdict = %v, want flag: a domain must not drop a segment by itself", seg.Verdict)
	}
	if res.SourceScore != 0 {
		t.Errorf("SourceScore = %v, want 0", res.SourceScore)
	}
	if seg.Score > 1 {
		t.Errorf("score = %v, want it clamped to 1", seg.Score)
	}
	if !contains(seg.Reasons, ReasonDomain) {
		t.Errorf("reasons = %v, want %q among them", seg.Reasons, ReasonDomain)
	}
	if len(cache.upserts) != 0 {
		t.Errorf("a flag is a question for the judge, not a cache row: %v", cache.upserts)
	}
}

func TestDomainDoesNotFlagPlainSegments(t *testing.T) {
	cfg := testConfig(t)
	cfg.L3.MaxShift = 1

	cache := newCache()
	cache.score = 0
	p := &Pipeline{Cfg: cfg, Cache: cache, Log: quiet()}

	res := run(t, p, core.Segment{ID: "s1", Text: plainText})
	if got := res.Segments[0].Verdict; got != core.Keep {
		t.Errorf("verdict = %v, want keep: no rule fired on this segment", got)
	}
}

func TestTrustedDomainLeavesScoreAlone(t *testing.T) {
	cache := newCache()
	p := &Pipeline{Cfg: testConfig(t), Cache: cache, Log: quiet()}

	res := run(t, p, core.Segment{ID: "s1", Text: plainText})
	if got := res.Segments[0].Verdict; got != core.Keep {
		t.Errorf("verdict = %v, want keep", got)
	}
	if contains(res.Segments[0].Reasons, ReasonDomain) {
		t.Errorf("a domain with nothing against it must add no reason: %v", res.Segments[0].Reasons)
	}
}

func TestDivergenceIsLogged(t *testing.T) {
	var buf bytes.Buffer
	cache := newCache()
	cache.put(adText, store.Record{Verdict: core.Keep, Source: core.SourceRules})
	p := &Pipeline{
		Cfg:   testConfig(t),
		Cache: cache,
		Log:   slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})),
	}

	run(t, p, core.Segment{ID: "s1", Text: adText, Links: adLinks})

	line := buf.String()
	for _, want := range []string{"diverges", "cached=keep", "local=drop", "id=s1"} {
		if !strings.Contains(line, want) {
			t.Errorf("log missing %q:\n%s", want, line)
		}
	}
}

func TestAgreementIsNotLogged(t *testing.T) {
	var buf bytes.Buffer
	cache := newCache()
	cache.put(adText, store.Record{Verdict: core.Drop, Source: core.SourceRules})
	p := &Pipeline{
		Cfg:   testConfig(t),
		Cache: cache,
		Log:   slog.New(slog.NewTextHandler(&buf, nil)),
	}

	run(t, p, core.Segment{ID: "s1", Text: adText, Links: adLinks})

	if buf.Len() != 0 {
		t.Errorf("nothing to report, got:\n%s", buf.String())
	}
}

func TestDropIsCached(t *testing.T) {
	cache := newCache()
	p := &Pipeline{Cfg: testConfig(t), Cache: cache, Log: quiet()}

	run(t, p,
		core.Segment{ID: "s1", Text: adText, Links: adLinks},
		core.Segment{ID: "s2", Text: plainText})

	if len(cache.upserts) != 1 {
		t.Fatalf("upserts = %d, want only the drop", len(cache.upserts))
	}
	got := cache.upserts[0]
	if got.Source != core.SourceRules || got.Verdict != core.Drop {
		t.Errorf("cached %v from %q, want drop from rules", got.Verdict, got.Source)
	}
	if !bytes.Equal(got.Hash, store.Hash(adText)) {
		t.Errorf("cached under the wrong hash")
	}
}

func TestBrokenCacheFallsBackToRules(t *testing.T) {
	cache := newCache()
	cache.err = errors.New("database is locked")
	p := &Pipeline{Cfg: testConfig(t), Cache: cache, Log: quiet()}

	if _, err := (&Pipeline{Cfg: testConfig(t), Cache: cache, Log: quiet()}).Run(
		core.Result{Domain: "example.com"}); err == nil {
		t.Error("a failing domain score must surface as an error")
	}

	res, err := p.Run(core.Result{Segments: []core.Segment{{ID: "s1", Text: adText, Links: adLinks}}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := res.Segments[0].Verdict; got != core.Drop {
		t.Errorf("verdict = %v, want drop: a dead cache must not disable the rules", got)
	}
}

func TestNoCacheRunsRulesOnly(t *testing.T) {
	p := &Pipeline{Cfg: testConfig(t), Log: quiet()}
	res := run(t, p, core.Segment{ID: "s1", Text: adText, Links: adLinks})

	if got := res.Segments[0].Verdict; got != core.Drop {
		t.Errorf("verdict = %v, want drop", got)
	}
	if res.SourceScore != 1 {
		t.Errorf("SourceScore = %v, want 1 without a store", res.SourceScore)
	}
}

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

type fakeShared struct {
	entries map[string]core.BucketEntry
	asked   [][]byte
}

func (s *fakeShared) put(text string, entry core.BucketEntry) {
	if s.entries == nil {
		s.entries = map[string]core.BucketEntry{}
	}
	key := hex.EncodeToString(store.Hash(text))
	entry.Hash = key
	s.entries[key] = entry
}

func (s *fakeShared) Lookup(hash []byte) (core.BucketEntry, bool) {
	s.asked = append(s.asked, hash)
	entry, ok := s.entries[hex.EncodeToString(hash)]
	return entry, ok
}

func TestSharedVerdictBeatsRulesAndIsCached(t *testing.T) {
	cache := newCache()
	shared := &fakeShared{}
	shared.put(adText, core.BucketEntry{Verdict: core.Keep, Reasons: []string{"voted not an ad"}, Source: core.SourceHuman})
	p := &Pipeline{Cfg: testConfig(t), Cache: cache, Shared: shared, Log: quiet()}

	res := run(t, p, core.Segment{ID: "s1", Text: adText, Links: adLinks})

	if got := res.Segments[0].Verdict; got != core.Keep {
		t.Errorf("verdict = %v, want keep — the rules ran over the shared database", got)
	}
	if got := res.Segments[0].Reasons; len(got) != 1 || got[0] != "voted not an ad" {
		t.Errorf("reasons = %v, want the shared ones", got)
	}
	if len(cache.upserts) != 1 || cache.upserts[0].Source != core.SourceShared {
		t.Fatalf("shared verdict not cached locally: %+v", cache.upserts)
	}
}

func TestSharedNotAskedAboveRules(t *testing.T) {
	cache := newCache()
	cache.put(adText, store.Record{Verdict: core.Keep, Source: core.SourceHuman})
	shared := &fakeShared{}
	p := &Pipeline{Cfg: testConfig(t), Cache: cache, Shared: shared, Log: quiet()}

	run(t, p, core.Segment{ID: "s1", Text: adText, Links: adLinks})

	if len(shared.asked) != 0 {
		t.Errorf("asked the shared database about a segment already decided locally: %d lookup(s)", len(shared.asked))
	}
}

func TestSharedOverridesCachedModelVerdict(t *testing.T) {
	cache := newCache()
	cache.put(adText, store.Record{Verdict: core.Drop, Source: core.SourceOllama})
	shared := &fakeShared{}
	shared.put(adText, core.BucketEntry{Verdict: core.Keep, Source: core.SourceHuman})
	p := &Pipeline{Cfg: testConfig(t), Cache: cache, Shared: shared, Log: quiet()}

	res := run(t, p, core.Segment{ID: "s1", Text: adText, Links: adLinks})

	if len(shared.asked) != 1 {
		t.Errorf("lookups = %d, want 1: a cached model verdict must not hide the shared database", len(shared.asked))
	}
	if got := res.Segments[0].Verdict; got != core.Keep {
		t.Errorf("verdict = %v, want keep from the shared database", got)
	}
}

func TestCachedModelVerdictSurvivesSharedMiss(t *testing.T) {
	cache := newCache()
	cache.put(plainText, store.Record{Verdict: core.Drop, Source: core.SourceOllama})
	p := &Pipeline{Cfg: testConfig(t), Cache: cache, Shared: &fakeShared{}, Log: quiet()}

	res := run(t, p, core.Segment{ID: "s1", Text: plainText})

	if got := res.Segments[0].Verdict; got != core.Drop {
		t.Errorf("verdict = %v, want the cached model verdict to still outrank the rules", got)
	}
}

func TestSharedMissFallsBackToRules(t *testing.T) {
	shared := &fakeShared{}
	p := &Pipeline{Cfg: testConfig(t), Cache: newCache(), Shared: shared, Log: quiet()}

	res := run(t, p, core.Segment{ID: "s1", Text: adText, Links: adLinks})

	if len(shared.asked) != 1 {
		t.Errorf("lookups = %d, want 1", len(shared.asked))
	}
	if got := res.Segments[0].Verdict; got != core.Drop {
		t.Errorf("verdict = %v, want drop from the rules", got)
	}
}

func TestSharedDivergenceIsCounted(t *testing.T) {
	var buf bytes.Buffer
	shared := &fakeShared{}
	shared.put(adText, core.BucketEntry{Verdict: core.Keep, Source: core.SourceHuman})
	shared.put(plainText, core.BucketEntry{Verdict: core.Keep, Source: core.SourceHuman})
	p := &Pipeline{
		Cfg:    testConfig(t),
		Cache:  newCache(),
		Shared: shared,
		Log:    slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})),
	}

	run(t, p,
		core.Segment{ID: "s1", Text: adText, Links: adLinks},
		core.Segment{ID: "s2", Text: plainText})

	if p.Diverged != 1 {
		t.Fatalf("Diverged = %d, want 1: the rules call s1 an ad and the database does not", p.Diverged)
	}
	for _, want := range []string{"diverges from shared", "id=s1", "shared=keep", "local=drop"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("log missing %q:\n%s", want, buf.String())
		}
	}
}

func TestAdoptedMarksVerdictsTakenFromElsewhere(t *testing.T) {
	shared := &fakeShared{}
	shared.put(adText, core.BucketEntry{Verdict: core.Drop, Source: core.SourceRules, Reasons: []string{"disclaimer"}})
	cache := newCache()
	cache.put(plainText, store.Record{Verdict: core.Drop, Source: core.SourceHuman})
	p := &Pipeline{Cfg: testConfig(t), Cache: cache, Shared: shared, Log: quiet()}

	const ownText = "Use code SAVE20 at checkout, sponsored by our partner, buy now."
	run(t, p,
		core.Segment{ID: "s1", Text: adText, Links: adLinks},
		core.Segment{ID: "s2", Text: plainText},
		core.Segment{ID: "s3", Text: ownText, Links: adLinks},
	)

	if !p.Adopted["s1"] || !p.Adopted["s2"] {
		t.Errorf("adopted = %v, want the shared and the human-cached segment in it", p.Adopted)
	}
	if p.Adopted["s3"] {
		t.Error("a segment the rules decided here must not count as adopted")
	}
}
