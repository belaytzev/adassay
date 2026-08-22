package pipeline

import (
	"testing"

	"github.com/belaytzev/adfilter/internal/core"
	"github.com/belaytzev/adfilter/internal/judge"
	"github.com/belaytzev/adfilter/internal/store"
)

// greyText fires disclaimer alone, which lands exactly on the grey zone with
// the default weights — the rules cannot decide and the judge gets it.
const greyText = "Sponsored post: our favourite grinder of the year."

type fakeJudge struct {
	verdicts map[string]core.Verdict
	topic    string
	seen     []string
	calls    int
}

func (j *fakeJudge) Decide(topic string, segs []core.Segment) map[string]core.Verdict {
	j.calls++
	j.topic = topic
	for _, s := range segs {
		j.seen = append(j.seen, s.ID)
	}
	return j.verdicts
}

func TestJudgeDecidesGreyZone(t *testing.T) {
	cache := newCache()
	j := &fakeJudge{verdicts: map[string]core.Verdict{"s1": core.Drop}}
	p := &Pipeline{Cfg: testConfig(t), Cache: cache, Judge: j, Log: quiet()}

	res, err := p.Run(core.Result{
		Title:    "How to pick a grinder",
		Domain:   "example.com",
		Segments: []core.Segment{{ID: "s1", Text: greyText}, {ID: "s2", Text: plainText}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := res.Segments[0].Verdict; got != core.Drop {
		t.Errorf("verdict = %v, want drop from the judge", got)
	}
	if !contains(res.Segments[0].Reasons, judge.Reason) {
		t.Errorf("reasons = %v, want %q among them", res.Segments[0].Reasons, judge.Reason)
	}
	if len(j.seen) != 1 || j.seen[0] != "s1" {
		t.Errorf("judge saw %v, want only the grey segment", j.seen)
	}
	if j.topic != "How to pick a grinder" {
		t.Errorf("topic = %q, want the document title", j.topic)
	}
	if len(cache.upserts) != 1 || cache.upserts[0].Source != core.SourceOllama {
		t.Fatalf("upserts = %+v, want one row from ollama", cache.upserts)
	}
	if got := cache.upserts[0].Hash; string(got) != string(store.Hash(greyText)) {
		t.Errorf("cached under the wrong hash")
	}
}

func TestJudgeSilenceLeavesFlag(t *testing.T) {
	for name, j := range map[string]Judge{
		"no judge":     nil,
		"no answer":    &fakeJudge{},
		"other ids":    &fakeJudge{verdicts: map[string]core.Verdict{"s9": core.Drop}},
		"empty answer": &fakeJudge{verdicts: map[string]core.Verdict{}},
	} {
		t.Run(name, func(t *testing.T) {
			cache := newCache()
			p := &Pipeline{Cfg: testConfig(t), Cache: cache, Judge: j, Log: quiet()}
			res := run(t, p, core.Segment{ID: "s1", Text: greyText})

			if got := res.Segments[0].Verdict; got != core.Flag {
				t.Errorf("verdict = %v, want flag", got)
			}
			if contains(res.Segments[0].Reasons, judge.Reason) {
				t.Errorf("reasons = %v, must not claim a judge verdict", res.Segments[0].Reasons)
			}
			if len(cache.upserts) != 0 {
				t.Errorf("an undecided flag is not a cache row: %v", cache.upserts)
			}
		})
	}
}

// A Flag that came from a human or the shared database is settled; re-asking a
// local model about it would let a heuristic overrule an authority.
func TestJudgeSkipsSettledSegments(t *testing.T) {
	cache := newCache()
	cache.put(greyText, store.Record{Verdict: core.Flag, Source: core.SourceHuman})
	j := &fakeJudge{verdicts: map[string]core.Verdict{"s1": core.Drop}}
	p := &Pipeline{Cfg: testConfig(t), Cache: cache, Judge: j, Log: quiet()}

	res := run(t, p, core.Segment{ID: "s1", Text: greyText})

	if j.calls != 0 {
		t.Errorf("judge was asked about a human verdict: %v", j.seen)
	}
	if got := res.Segments[0].Verdict; got != core.Flag {
		t.Errorf("verdict = %v, want the cached flag", got)
	}
}

func TestJudgeIsNotAskedWithoutGreyZone(t *testing.T) {
	j := &fakeJudge{}
	p := &Pipeline{Cfg: testConfig(t), Judge: j, Log: quiet()}

	run(t, p, core.Segment{ID: "s1", Text: plainText}, core.Segment{ID: "s2", Text: adText, Links: adLinks})

	if j.calls != 0 {
		t.Errorf("judge called %d times with nothing to judge", j.calls)
	}
}
