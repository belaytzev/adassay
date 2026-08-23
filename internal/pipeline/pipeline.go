package pipeline

import (
	"log/slog"

	"adassay.com/internal/config"
	"adassay.com/internal/core"
	"adassay.com/internal/judge"
	"adassay.com/internal/rules"
	"adassay.com/internal/store"
)

const ReasonDomain = "domain_distrust"

type Cache interface {
	Lookup(hash []byte) (store.Record, bool, error)
	Upsert(rec store.Record) error
	Source(domain string, l3 config.L3) (float64, error)
}

type Judge interface {
	Decide(topic string, segs []core.Segment) map[string]core.Verdict
}

type Shared interface {
	Lookup(hash []byte) (core.BucketEntry, bool)
}

type Pipeline struct {
	Cfg    *config.Config
	Cache  Cache
	Shared Shared
	Judge  Judge
	Log    *slog.Logger

	Diverged int

	Adopted map[string]bool
}

var priority = map[string]int{
	core.SourceRules:  0,
	core.SourceOllama: 1,
	core.SourceShared: 2,
	core.SourceHuman:  3,
}

func Outranks(a, b string) bool { return priority[a] > priority[b] }

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
		if settled {
			if p.Adopted == nil {
				p.Adopted = map[string]bool{}
			}
			p.Adopted[decided.ID] = true
			continue
		}
		if decided.Verdict == core.Flag {
			grey = append(grey, i)
		}
	}
	p.ask(res, grey)
	return res, nil
}

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

func (p *Pipeline) segment(seg core.Segment, doc rules.Doc, sourceScore float64) (core.Segment, bool) {
	hash := store.Hash(seg.Text)
	cached, hit := p.lookup(hash)

	if hit && !Outranks(core.SourceShared, cached.Source) {
		seg.Verdict = cached.Verdict
		seg.Reasons = cached.Reasons
		return seg, true
	}

	if entry, ok := p.lookupShared(hash); ok {

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

	if hit && Outranks(cached.Source, core.SourceRules) {
		seg.Verdict = cached.Verdict
		seg.Reasons = cached.Reasons
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

func (p *Pipeline) shift(seg core.Segment, sourceScore float64) core.Segment {

	if sourceScore >= 1 || len(seg.Reasons) == 0 {
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
