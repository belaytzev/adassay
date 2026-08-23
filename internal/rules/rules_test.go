package rules

import (
	"math"
	"testing"

	"adassay.com/internal/config"
	"adassay.com/internal/core"
)

func l2(t *testing.T) config.L2 {
	t.Helper()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return cfg.L2
}

func TestScoreRangeAndBias(t *testing.T) {
	c := l2(t)
	none := Score(nil, c)
	if none <= 0 || none >= 1 {
		t.Fatalf("empty score %v is outside 0..1", none)
	}
	if want := 1 / (1 + math.Exp(-c.Bias)); math.Abs(none-want) > 1e-9 {
		t.Fatalf("empty score = %v, want sigmoid(bias) = %v", none, want)
	}
	if all := Score(config.Features, c); all <= none {
		t.Fatalf("all features scored %v, not above empty %v", all, none)
	}
}

// Adding a fired feature must never lower the score, or a segment could be
// cleared by looking more like an ad.
func TestScoreMonotonic(t *testing.T) {
	c := l2(t)
	for mask := 0; mask < 1<<len(config.Features); mask++ {
		var base []string
		for i, f := range config.Features {
			if mask&(1<<i) != 0 {
				base = append(base, f)
			}
		}
		got := Score(base, c)
		for i, f := range config.Features {
			if mask&(1<<i) != 0 {
				continue
			}
			if more := Score(append(append([]string{}, base...), f), c); more < got {
				t.Fatalf("adding %q to %v lowered score: %v -> %v", f, base, got, more)
			}
		}
	}
}

func TestClassifyThresholds(t *testing.T) {
	c := l2(t)
	cases := []struct {
		name  string
		score float64
		want  core.Verdict
	}{
		{"far below lo", 0, core.Keep},
		{"just below lo", c.Lo - 1e-9, core.Keep},
		{"exactly lo", c.Lo, core.Flag},
		{"between", (c.Lo + c.Hi) / 2, core.Flag},
		{"exactly hi", c.Hi, core.Flag},
		{"just above hi", c.Hi + 1e-9, core.Drop},
		{"far above hi", 1, core.Drop},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.score, c); got != tc.want {
				t.Fatalf("Classify(%v) = %v, want %v", tc.score, got, tc.want)
			}
		})
	}
}

// The shortcut must not depend on the arithmetic: zero every weight and drive
// the bias to the floor, and rel_sponsored + promo_code still drops.
func TestShortcutBypassesWeights(t *testing.T) {
	c := l2(t)
	c.Bias = -50
	c.Weights = map[string]float64{}
	for _, f := range config.Features {
		c.Weights[f] = 0
	}
	features := []string{config.FeatureRelSponsored, config.FeaturePromoCode}
	if s := Score(features, c); Classify(s, c) != core.Keep {
		t.Fatalf("arithmetic did not say Keep (score %v), test proves nothing", s)
	}
	v, ok := Shortcut(features, c)
	if !ok || v != core.Drop {
		t.Fatalf("Shortcut = (%v, %v), want (drop, true)", v, ok)
	}
	if got := Apply(core.Segment{
		Text:  "Use code SAVE20 at checkout.",
		Links: []core.Link{{Href: "https://shop.example/x", Rel: "sponsored"}},
	}, Doc{}, c).Verdict; got != core.Drop {
		t.Fatalf("Apply verdict = %v, want drop", got)
	}
}

func TestShortcutNeedsEveryFeature(t *testing.T) {
	c := l2(t)
	if _, ok := Shortcut([]string{config.FeatureRelSponsored}, c); ok {
		t.Fatal("shortcut fired on a partial match")
	}
	if _, ok := Shortcut(nil, c); ok {
		t.Fatal("shortcut fired on no features")
	}
}

func TestApply(t *testing.T) {
	c := l2(t)
	clean := Apply(core.Segment{
		Text:  "The compiler rewrites the loop into a single pass over the slice.",
		Links: []core.Link{{Href: "https://go.dev/doc"}},
	}, Doc{}, c)
	if clean.Verdict != core.Keep {
		t.Fatalf("clean segment verdict = %v, want keep", clean.Verdict)
	}
	if len(clean.Reasons) != 0 {
		t.Fatalf("clean segment has reasons %v", clean.Reasons)
	}

	ad := Apply(core.Segment{
		Text:  "На правах рекламы: подписка со скидкой.",
		Links: []core.Link{{Href: "https://go.skimresources.com/?id=1"}},
	}, Doc{}, c)
	if ad.Verdict != core.Drop {
		t.Fatalf("ad segment verdict = %v (score %v), want drop", ad.Verdict, ad.Score)
	}
	if len(ad.Reasons) == 0 {
		t.Fatal("ad segment carries no reasons")
	}
}
