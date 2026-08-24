package main

import (
	"testing"

	"adassay.com/internal/config"
)

const corpusDir = "../../testdata/corpus"

const (
	minRecall   = 0.55
	minCoverage = 1.00
	maxNoise    = 83
)

const maxHiddenFalsePositives = 3

func TestCorpusRegression(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	ev, err := evalCorpus(corpusDir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev.absent) > 0 {
		t.Skipf("%d captured pages are not downloaded: run testdata/corpus/fetch.sh", len(ev.absent))
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

func TestNativeDropIsNotAMistake(t *testing.T) {
	ev := evaluation{segments: []scored{
		{page: "p", id: "s1", score: 0.9, native: true},
		{page: "p", id: "s2", score: 0.9, ad: true},
		{page: "p", id: "s3", score: 0.9},
	}}
	m := score(ev, 0.55, 0.10)
	if m.tp != 2 {
		t.Errorf("tp = %d, want 2: a drop on native is a hit, not a mistake", m.tp)
	}
	if m.fp != 1 {
		t.Errorf("fp = %d, want 1: only the unlabelled drop counts against precision", m.fp)
	}
}

func TestFlagOnAdCounts(t *testing.T) {
	ev := evaluation{segments: []scored{
		{page: "p", id: "s1", score: 0.30, ad: true},
		{page: "p", id: "s2", score: 0.30, native: true},
	}}
	m := score(ev, 0.55, 0.10)
	if m.coverage() != 1.0 {
		t.Errorf("coverage = %.3f, want 1.000: a flagged ad is caught, not missed", m.coverage())
	}
	if m.nativeCoverage() != 1.0 {
		t.Errorf("nativeCoverage = %.3f, want 1.000", m.nativeCoverage())
	}
	if m.flags != 2 {
		t.Errorf("flags = %d, want 2", m.flags)
	}
}

func TestBetterRejectsLostFacts(t *testing.T) {
	lossy := metrics{tp: 9, fp: 1, covered: 10, ads: 10, segments: 100, flags: 5}
	safe := metrics{tp: 5, fp: 0, covered: 5, ads: 10, segments: 100, flags: 5}
	if better(lossy, safe) {
		t.Error("a config that cuts a fact must lose to one that never does, whatever its coverage")
	}

	noisy := metrics{tp: 10, fp: 0, covered: 10, ads: 10, segments: 100, flags: 99}
	quiet := metrics{tp: 8, fp: 0, covered: 8, ads: 10, segments: 100, flags: 5}
	if better(noisy, quiet) {
		t.Error("flagging almost everything must lose: it warns about nothing")
	}
}
