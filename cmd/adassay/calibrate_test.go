package main

import (
	"testing"

	"adassay.com/internal/config"
)

const corpusDir = "../../testdata/corpus"

const (
	minRecall   = 0.55
	minCoverage = 1.00
	maxNoise    = 7
)

const maxHiddenFalsePositives = 2

func TestCorpusRegression(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	ev, err := evalCorpus(corpusDir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	m := score(ev, cfg.L2.Hi, cfg.L2.Lo)
	if m.ads == 0 {
		t.Fatal("corpus has no advertising labels")
	}
	if m.precision() < MinPrecision {
		t.Errorf("precision %.3f below floor %.2f", m.precision(), MinPrecision)
	}
	if m.recall() < minRecall {
		t.Errorf("recall %.3f below floor %.2f", m.recall(), minRecall)
	}
	if m.coverage() < minCoverage {
		t.Errorf("coverage %.3f below floor %.2f", m.coverage(), minCoverage)
	}
	if m.noise > maxNoise {
		t.Errorf("%d honest segments flagged, baseline %d", m.noise, maxNoise)
	}
}

func TestCorpusHidden(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	ev, err := evalCorpus(corpusDir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	falsePositives := 0
	for _, p := range ev.pages {
		switch {
		case p.label.Hidden && !p.hidden:
			t.Errorf("%s: injection not detected", p.label.File)
		case !p.label.Hidden && p.hidden:
			falsePositives++
		}
	}
	if falsePositives > maxHiddenFalsePositives {
		t.Errorf("%d pages fire l1 without an injection, baseline %d", falsePositives, maxHiddenFalsePositives)
	}
}

func TestMatchesLabel(t *testing.T) {
	long := "Best Budget Laptop Overall: HP Pavilion 15.6-inch Laptop"
	tests := []struct {
		name  string
		flat  string
		label string
		want  bool
	}{
		{"long prefix matches", long + " is available in several configurations.", long, true},
		{"long prefix rejects other text", "Something else entirely, at length, with padding.", long, false},
		{"short label needs the whole segment", "NordVPN Basic", "NordVPN Basic", true},
		{"short label does not match a prefix", "NordVPN Basic is just the VPN software.", "NordVPN Basic", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesLabel(tt.flat, tt.label); got != tt.want {
				t.Errorf("matchesLabel(%q, %q) = %v, want %v", tt.flat, tt.label, got, tt.want)
			}
		})
	}
}
