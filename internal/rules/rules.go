package rules

import (
	"math"

	"github.com/belaytzev/adfilter/internal/config"
	"github.com/belaytzev/adfilter/internal/core"
)

// Apply runs L2 over a segment: detect the deterministic features, score them
// and set the verdict. The returned segment carries the fired features as its
// reasons, so a Flag stays explainable downstream.
func Apply(seg core.Segment, doc Doc, l2 config.L2) core.Segment {
	features := Detect(seg, doc, l2.Patterns)
	seg.Reasons = features
	seg.Score = Score(features, l2)
	if v, ok := Shortcut(features, l2); ok {
		seg.Verdict = v
		return seg
	}
	seg.Verdict = Classify(seg.Score, l2)
	return seg
}

// Score is the weighted sum of the fired features squashed into 0..1. Weights
// are non-negative by config validation, so firing one more feature can only
// raise the score.
func Score(features []string, l2 config.L2) float64 {
	sum := l2.Bias
	for _, f := range features {
		sum += l2.Weights[f]
	}
	return 1 / (1 + math.Exp(-sum))
}

// Shortcut bypasses the arithmetic: a combination that is an outright
// admission of paid placement gets its verdict regardless of the weights.
func Shortcut(features []string, l2 config.L2) (core.Verdict, bool) {
	fired := make(map[string]bool, len(features))
	for _, f := range features {
		fired[f] = true
	}
	for _, s := range l2.Shortcuts {
		if all(fired, s.Features) {
			return s.ParsedVerdict(), true
		}
	}
	return core.Keep, false
}

// Classify splits the score on the configured thresholds. The boundaries
// themselves fall into the grey zone: on an exact hi or lo the rules are not
// confident enough to decide alone, and the segment is left to L3 and judge.
func Classify(score float64, l2 config.L2) core.Verdict {
	switch {
	case score > l2.Hi:
		return core.Drop
	case score < l2.Lo:
		return core.Keep
	default:
		return core.Flag
	}
}

func all(fired map[string]bool, want []string) bool {
	for _, f := range want {
		if !fired[f] {
			return false
		}
	}
	return true
}
