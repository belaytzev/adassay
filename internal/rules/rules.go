package rules

import (
	"math"

	"adassay.com/internal/config"
	"adassay.com/internal/core"
)

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

func Score(features []string, l2 config.L2) float64 {
	sum := l2.Bias
	for _, f := range features {
		sum += l2.Weights[f]
	}
	return 1 / (1 + math.Exp(-sum))
}

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
