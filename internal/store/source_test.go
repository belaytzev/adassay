package store

import (
	"math"
	"testing"
	"time"

	"adassay.com/internal/config"
)

var l3 = config.L3{MinVisits: 5, HalfLifeDays: 30, FindingPenalty: 0.25, MaxShift: 0.2}

func TestSourceScore(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name  string
		st    DomainStats
		want  float64
		delta float64
	}{
		{"clean domain", DomainStats{Visits: 50, UpdatedAt: now}, 1, 0},
		{"below min visits", DomainStats{Visits: 4, Findings: 4, UpdatedAt: now}, 1, 0},
		{"one finding in many visits", DomainStats{Visits: 100, Findings: 1, UpdatedAt: now}, 0.997, 0.005},
		{"finding on every page", DomainStats{Visits: 20, Findings: 20, UpdatedAt: now}, 0.75, 0.001},
		{"several findings per page", DomainStats{Visits: 20, Findings: 80, UpdatedAt: now}, 0.316, 0.005},
		{"stale evidence", DomainStats{Visits: 20, Findings: 20, UpdatedAt: now.AddDate(0, 0, -365)}, 1, 0.001},
		{"empty stats", DomainStats{}, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SourceScore(tt.st, l3, now)
			if math.Abs(got-tt.want) > tt.delta {
				t.Fatalf("SourceScore = %v, want %v ± %v", got, tt.want, tt.delta)
			}
			if got < 0 || got > 1 {
				t.Fatalf("SourceScore = %v, outside 0..1", got)
			}
		})
	}
}

// A single finding must not bury a domain, however small the sample it is
// allowed to judge.
func TestSourceScoreSurvivesOneFinding(t *testing.T) {
	now := time.Now()
	got := SourceScore(DomainStats{Visits: 5, Findings: 1, UpdatedAt: now}, l3, now)
	if got < 0.9 {
		t.Fatalf("SourceScore = %v, one finding in five visits must stay near trust", got)
	}
}

func TestSourceScoreDecays(t *testing.T) {
	now := time.Now()
	st := DomainStats{Visits: 20, Findings: 20}

	prev := 0.0
	for _, days := range []int{0, 30, 60, 120, 365} {
		st.UpdatedAt = now.AddDate(0, 0, -days)
		got := SourceScore(st, l3, now)
		if got <= prev {
			t.Fatalf("score at %d days = %v, want above the previous %v", days, got, prev)
		}
		prev = got
	}
	if prev > 1 {
		t.Fatalf("decayed score = %v, want at most 1", prev)
	}

	st.UpdatedAt = now.AddDate(0, 0, -30)
	half := SourceScore(st, l3, now)
	if want := math.Pow(1-l3.FindingPenalty, 0.5); math.Abs(half-want) > 0.001 {
		t.Fatalf("score after one half-life = %v, want %v", half, want)
	}
}

func TestStoreSource(t *testing.T) {
	s := open(t)
	for range 10 {
		if err := s.Visit("promo.example", 1); err != nil {
			t.Fatalf("Visit: %v", err)
		}
	}
	got, err := s.Source("promo.example", l3)
	if err != nil {
		t.Fatalf("Source: %v", err)
	}
	if math.Abs(got-0.75) > 0.001 {
		t.Fatalf("Source = %v, want 0.75", got)
	}
	if unseen, err := s.Source("clean.example", l3); err != nil || unseen != 1 {
		t.Fatalf("Source of an unseen domain = %v, %v, want 1, nil", unseen, err)
	}
}
