// Package pipeline wires the layers into one pass over a page: the local
// verdict cache answers first, L2 rules score only what it does not know, and
// the L3 domain score nudges what the rules were unsure about.
package pipeline

import (
	"log/slog"

	"github.com/belaytzev/adfilter/internal/config"
	"github.com/belaytzev/adfilter/internal/core"
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

type Pipeline struct {
	Cfg   *config.Config
	Cache Cache
	Log   *slog.Logger
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
// domain score. Segments left in the grey zone stay Flag: the judge picks them
// up later, and until then a marker is a more honest answer than a guess.
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
	for i, seg := range res.Segments {
		res.Segments[i] = p.segment(seg, score)
	}
	return res, nil
}

func (p *Pipeline) segment(seg core.Segment, sourceScore float64) core.Segment {
	hash := store.Hash(seg.Text)
	cached, hit := p.lookup(hash)

	// A cached verdict from a source above the rules is the answer, and the
	// rules never run: re-scoring would risk overriding a human with a
	// heuristic. Our own past output is not an authority, so a rules row is
	// re-scored — and the disagreement is worth a line in the log.
	if hit && Outranks(cached.Source, core.SourceRules) {
		seg.Verdict = cached.Verdict
		seg.Reasons = cached.Reasons
		return seg
	}

	seg = rules.Apply(seg, p.Cfg.L2)
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
	return seg
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
	if p.Cache == nil || seg.Verdict != core.Drop {
		return
	}
	rec := store.Record{
		Hash:    hash,
		Verdict: seg.Verdict,
		Reasons: seg.Reasons,
		Source:  core.SourceRules,
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
