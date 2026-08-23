package store

import (
	"math"
	"time"

	"adassay.com/internal/config"
)

func SourceScore(st DomainStats, l3 config.L3, now time.Time) float64 {

	if st.Visits < l3.MinVisits || st.Findings <= 0 {
		return 1
	}
	rate := float64(st.Findings) / float64(st.Visits) * decay(st.UpdatedAt, now, l3.HalfLifeDays)

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

func (s *Store) Source(domain string, l3 config.L3) (float64, error) {
	st, err := s.Domain(domain)
	if err != nil {
		return 1, err
	}
	return SourceScore(st, l3, time.Now()), nil
}
