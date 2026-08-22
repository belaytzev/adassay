package store

import (
	"math"
	"time"

	"github.com/belaytzev/adfilter/internal/config"
)

// SourceScore is the L3 trust of a domain in 0..1, where 1 is a source with
// nothing held against it. The pipeline turns distrust into an additive shift
// of the segment score, so a bad domain nudges borderline segments and never
// decides a verdict alone.
func SourceScore(st DomainStats, l3 config.L3, now time.Time) float64 {
	// Too little history is not evidence: a domain seen twice says nothing
	// about the third page.
	if st.Visits < l3.MinVisits || st.Findings <= 0 {
		return 1
	}
	rate := float64(st.Findings) / float64(st.Visits) * decay(st.UpdatedAt, now, l3.HalfLifeDays)

	// Geometric penalty: every finding per visit takes the same share of the
	// remaining trust, so the score approaches zero without ever reaching it.
	// One hacked UGC post costs a domain a slice, not its life.
	penalty := math.Min(math.Max(l3.FindingPenalty, 0), 0.99)
	return math.Pow(1-penalty, rate)
}

func decay(updated, now time.Time, halfLifeDays float64) float64 {
	if updated.IsZero() || halfLifeDays <= 0 {
		return 1
	}
	days := now.Sub(updated).Hours() / 24
	if days <= 0 {
		return 1
	}
	return math.Exp2(-days / halfLifeDays)
}

// Source reads the counters of a domain and scores them.
func (s *Store) Source(domain string, l3 config.L3) (float64, error) {
	st, err := s.Domain(domain)
	if err != nil {
		return 1, err
	}
	return SourceScore(st, l3, time.Now()), nil
}
