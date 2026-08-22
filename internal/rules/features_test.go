package rules

import (
	"strings"
	"testing"

	"github.com/belaytzev/adfilter/internal/config"
	"github.com/belaytzev/adfilter/internal/core"
)

func patterns(t *testing.T) config.Patterns {
	t.Helper()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return cfg.L2.Patterns
}

func fires(t *testing.T, feature string, seg core.Segment) bool {
	t.Helper()
	for _, f := range Detect(seg, patterns(t)) {
		if f == feature {
			return true
		}
	}
	return false
}

func TestRelSponsored(t *testing.T) {
	cases := []struct {
		name string
		rel  string
		want bool
	}{
		{"plain", "sponsored", true},
		{"token list", "nofollow sponsored noopener", true},
		{"upper case", "Sponsored", true},
		{"empty", "", false},
		{"nofollow only", "nofollow", false},
		{"substring is not a token", "unsponsored", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			seg := core.Segment{Text: "a review", Links: []core.Link{{Href: "https://shop.example/x", Rel: c.rel}}}
			if got := fires(t, config.FeatureRelSponsored, seg); got != c.want {
				t.Errorf("rel=%q fired = %v, want %v", c.rel, got, c.want)
			}
		})
	}
}

func TestPromoCode(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"use code", "Use code SAVE20 at checkout for 20% off.", true},
		{"promo code", "Our promo code ADFILTER10 works until Friday.", true},
		{"russian", "Действует промокод ЛЕТО2026 на первый заказ.", true},
		{"code before the word", "SAVE20 is the discount code you need.", true},
		{"word without a code", "Enter the discount code you were given at checkout.", false},
		{"code without the word", "The HTTP response was 200 OK for SAVE20.", false},
		{"code too far away", "Use code " + strings.Repeat("word ", 20) + "SAVE20", false},
		{"programming prose", "This code snippet parses JSON and HTML without a promo anywhere.", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fires(t, config.FeaturePromoCode, core.Segment{Text: c.text}); got != c.want {
				t.Errorf("%q fired = %v, want %v", c.text, got, c.want)
			}
		})
	}
}

func TestAffiliateLink(t *testing.T) {
	cases := []struct {
		name string
		href string
		want bool
	}{
		{"amazon tag", "https://www.amazon.com/dp/B01?tag=blog-20", true},
		{"ref param", "https://shop.example/item?ref=habr", true},
		{"utm_campaign", "https://shop.example/item?utm_source=x&utm_campaign=affiliate", true},
		{"param case", "https://shop.example/item?Ref=habr", true},
		{"skimlinks redirector", "https://go.skimresources.com/?id=1&url=x", true},
		{"awin redirector", "https://www.awin1.com/cread.php?awinmid=1", true},
		{"impact subdomain", "https://track.impact.com/c/1/2", true},
		{"clean link", "https://shop.example/item?color=red", false},
		{"host that merely ends alike", "https://notimpact.com/c/1", false},
		{"relative link", "/blog/post", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			seg := core.Segment{Text: "buy it", Links: []core.Link{{Href: c.href}}}
			if got := fires(t, config.FeatureAffiliate, seg); got != c.want {
				t.Errorf("%q fired = %v, want %v", c.href, got, c.want)
			}
		})
	}
}

func TestDisclaimer(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"hashtag ad", "New drop from the brand. #ad", true},
		{"in partnership with", "This guide was made in partnership with Acme.", true},
		{"paid partnership", "Paid partnership with Acme Cloud.", true},
		{"russian legal", "На правах рекламы. Компания предлагает тариф.", true},
		{"russian partner material", "Партнёрский материал подготовлен вместе с Acme.", true},
		{"hashtag prefix of a longer tag", "Follow us at #adfilter for updates.", false},
		{"word prefix", "Рекламация была отклонена поставщиком.", false},
		{"neutral prose", "The team compared three storage engines on the same hardware.", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fires(t, config.FeatureDisclaimer, core.Segment{Text: c.text}); got != c.want {
				t.Errorf("%q fired = %v, want %v", c.text, got, c.want)
			}
		})
	}
}

func TestDetectOrderAndIndependence(t *testing.T) {
	seg := core.Segment{
		Text:  "На правах рекламы: используйте промокод SAVE20.",
		Links: []core.Link{{Href: "https://shop.example/x?ref=blog", Rel: "sponsored"}},
	}
	got := Detect(seg, patterns(t))
	want := config.Features
	if len(got) != len(want) {
		t.Fatalf("Detect = %v, want all of %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Detect[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if got := Detect(core.Segment{Text: "A plain paragraph about databases."}, patterns(t)); len(got) != 0 {
		t.Errorf("Detect on neutral text = %v, want none", got)
	}
}
