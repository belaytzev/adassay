// Package pipeline wires the layers into one pass over a page: the local
// verdict cache answers first, L2 rules score only what it does not know, and
// the L3 domain score nudges what the rules were unsure about.
package pipeline

import (
	"log/slog"

	"github.com/belaytzev/adfilter/internal/config"
	"github.com/belaytzev/adfilter/internal/core"
	"github.com/belaytzev/adfilter/internal/judge"
	"github.com/belaytzev/adfilter/internal/rules"
	"github.com/belaytzev/adfilter/internal/store"
)

// ReasonDomain marks a verdict the domain score pushed up, so a Flag raised by
// distrust is distinguishable from one the rules raised on their own.
const ReasonDomain = "domain_distrust"

// Cache is the part of the local store the pipeline needs.
type Cache interface {
	Lookup(hash []byte) (store.Record, bool, error)
	Upsert(rec store.Record) error
	Source(domain string, l3 config.L3) (float64, error)
}

// Judge is the grey-zone arbiter, kept as an interface so a run without a local
// model is a nil field rather than a stub server.
type Judge interface {
	Decide(topic string, segs []core.Segment) map[string]core.Verdict
}

// Shared is the read side of the shared verdict database. A nil field is a run
// that asks nobody anything.
type Shared interface {
	Lookup(hash []byte) (core.BucketEntry, bool)
}

type Pipeline struct {
	Cfg    *config.Config
	Cache  Cache
	Shared Shared
	Judge  Judge
	Log    *slog.Logger

	// Diverged counts segments where the local rules disagreed with the shared
	// database. A run full of divergences means the heuristics drifted.
	Diverged int
}

// priority resolves two verdicts for the same segment: a human override beats
// the shared database, which beats a local model, which beats the rules.
var priority = map[string]int{
	core.SourceRules:  0,
	core.SourceOllama: 1,
	core.SourceShared: 2,
	core.SourceHuman:  3,
}

func Outranks(a, b string) bool { return priority[a] > priority[b] }

// Run decides every segment of an already extracted page and fills in the
// domain score. What the rules leave in the grey zone goes to the judge, and
// whatever the judge does not answer stays Flag: a marker is a more honest
// answer than a guess.
func (p *Pipeline) Run(res core.Result) (core.Result, error) {
	score := 1.0
	if p.Cache != nil && res.Domain != "" {
		s, err := p.Cache.Source(res.Domain, p.Cfg.L3)
		if err != nil {
			return res, err
		}
		score = s
	}
	res.SourceScore = score
	doc := rules.NewDoc(res.Segments)
	var grey []int
	for i, seg := range res.Segments {
		decided, settled := p.segment(seg, doc, score)
		res.Segments[i] = decided
		if !settled && decided.Verdict == core.Flag {
			grey = append(grey, i)
		}
	}
	p.ask(res, grey)
	return res, nil
}

// ask hands the grey zone to the local model. Segments it says nothing about
// keep their Flag: an unreachable model is a missing opinion, not an error.
func (p *Pipeline) ask(res core.Result, grey []int) {
	if p.Judge == nil || len(grey) == 0 {
		return
	}
	batch := make([]core.Segment, len(grey))
	for i, idx := range grey {
		batch[i] = res.Segments[idx]
	}
	verdicts := p.Judge.Decide(res.Title, batch)
	for _, idx := range grey {
		seg := &res.Segments[idx]
		v, ok := verdicts[seg.ID]
		if !ok || v == seg.Verdict {
			continue
		}
		seg.Verdict = v
		seg.Reasons = append(seg.Reasons, judge.Reason)
		if v == core.Drop {
			p.store(store.Hash(seg.Text), *seg, core.SourceOllama)
		}
	}
}

// segment returns the decided segment and whether the verdict came from an
// authority above the rules, which nothing downstream may revisit.
func (p *Pipeline) segment(seg core.Segment, doc rules.Doc, sourceScore float64) (core.Segment, bool) {
	hash := store.Hash(seg.Text)
	cached, hit := p.lookup(hash)

	// A cached verdict from a source above the rules is the answer, and the
	// rules never run: re-scoring would risk overriding a human with a
	// heuristic. Our own past output is not an authority, so a rules row is
	// re-scored — and the disagreement is worth a line in the log.
	if hit && Outranks(cached.Source, core.SourceRules) {
		seg.Verdict = cached.Verdict
		seg.Reasons = cached.Reasons
		return seg, true
	}

	// The shared database is asked only for what the local cache could not
	// answer with authority, and its answer is cached so the next run of the
	// same segment stays offline.
	if entry, ok := p.lookupShared(hash); ok {
		// The rules run anyway and their answer is thrown away: how often the
		// local verdict disagrees with the database is the only measure of
		// whether the heuristics still track what everyone else derives.
		if local := p.shift(rules.Apply(seg, doc, p.Cfg.L2), sourceScore); local.Verdict != entry.Verdict {
			p.Diverged++
			p.log().Warn("verdict diverges from shared database",
				"id", seg.ID,
				"shared", entry.Verdict.String(),
				"local", local.Verdict.String(),
				"score", local.Score)
		}
		seg.Verdict = entry.Verdict
		seg.Reasons = entry.Reasons
		p.store(hash, seg, core.SourceShared)
		return seg, true
	}

	seg = rules.Apply(seg, doc, p.Cfg.L2)
	seg = p.shift(seg, sourceScore)

	if hit && cached.Verdict != seg.Verdict {
		p.log().Warn("verdict diverges from cache",
			"id", seg.ID,
			"cached", cached.Verdict.String(),
			"cached_source", cached.Source,
			"local", seg.Verdict.String(),
			"score", seg.Score)
	}
	p.remember(hash, seg)
	return seg, false
}

// shift is the L3 modifier: distrust in the domain is an additive push on the
// segment score, clamped to 0..1. It escalates by at most one step, so a domain
// with a zero score raises suspicion and never convicts on its own.
func (p *Pipeline) shift(seg core.Segment, sourceScore float64) core.Segment {
	if sourceScore >= 1 {
		return seg
	}
	before := seg.Verdict
	seg.Score = min(seg.Score+p.Cfg.L3.MaxShift*(1-sourceScore), 1)

	after := rules.Classify(seg.Score, p.Cfg.L2)
	if before == core.Keep && after == core.Drop {
		after = core.Flag
	}
	if after > before {
		seg.Verdict = after
		seg.Reasons = append(seg.Reasons, ReasonDomain)
	}
	return seg
}

func (p *Pipeline) lookupShared(hash []byte) (core.BucketEntry, bool) {
	if p.Shared == nil {
		return core.BucketEntry{}, false
	}
	return p.Shared.Lookup(hash)
}

func (p *Pipeline) lookup(hash []byte) (store.Record, bool) {
	if p.Cache == nil {
		return store.Record{}, false
	}
	rec, ok, err := p.Cache.Lookup(hash)
	if err != nil {
		p.log().Warn("cache lookup failed", "err", err)
		return store.Record{}, false
	}
	return rec, ok
}

// remember caches confident findings only: a Keep is the default answer and a
// Flag is a question left for the judge, neither is worth a row.
func (p *Pipeline) remember(hash []byte, seg core.Segment) {
	if seg.Verdict != core.Drop {
		return
	}
	p.store(hash, seg, core.SourceRules)
}

func (p *Pipeline) store(hash []byte, seg core.Segment, source string) {
	if p.Cache == nil {
		return
	}
	rec := store.Record{
		Hash:    hash,
		Verdict: seg.Verdict,
		Reasons: seg.Reasons,
		Source:  source,
	}
	if err := p.Cache.Upsert(rec); err != nil {
		p.log().Warn("cache upsert failed", "id", seg.ID, "err", err)
	}
}

func (p *Pipeline) log() *slog.Logger {
	if p.Log == nil {
		return slog.Default()
	}
	return p.Log
}
