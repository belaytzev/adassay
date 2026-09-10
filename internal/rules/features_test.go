package rules

import (
	"fmt"
	"strings"
	"testing"

	"adassay.com/internal/config"
	"adassay.com/internal/core"
)

func patterns(t *testing.T) config.Patterns {
	t.Helper()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return cfg.L2.Patterns
}

func fires(t *testing.T, feature string, seg core.Segment, doc Doc) bool {
	t.Helper()
	for _, f := range Detect(seg, doc, patterns(t)) {
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
			if got := fires(t, config.FeatureRelSponsored, seg, Doc{}); got != c.want {
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
		{"promo code", "Our promo code ACME10 works until Friday.", true},
		{"russian", "Действует промокод ЛЕТО2026 на первый заказ.", true},
		{"code before the word", "SAVE20 is the discount code you need.", true},
		{"word without a code", "Enter the discount code you were given at checkout.", false},
		{"code without the word", "The HTTP response was 200 OK for SAVE20.", false},
		{"code too far away", "Use code " + strings.Repeat("word ", 20) + "SAVE20", false},
		{"programming prose", "This code snippet parses JSON and HTML without a promo anywhere.", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fires(t, config.FeaturePromoCode, core.Segment{Text: c.text}, Doc{}); got != c.want {
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
		{"amazon sitestripe", "https://www.amazon.com/dp/B01?linkCode=ogi&th=1", true},
		{"param case", "https://shop.example/item?Aff_Id=7", true},
		{"wordpress tag filter", "https://blog.example/?tag=golang", false},
		{"ref is source tracking", "https://shop.example/item?ref=habr", false},
		{"utm is analytics", "https://shop.example/item?utm_source=x&utm_campaign=affiliate", false},
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
			if got := fires(t, config.FeatureAffiliate, seg, Doc{}); got != c.want {
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
		{"russian legal marking with prose", "Реклама. 12+. ООО «Единое Видео». VK Видео: vkvideo.ru", true},
		{"bare label", "Реклама", false},
		{"bare label punctuated", "#ad.", false},
		{"hashtag prefix of a longer tag", "Follow us at #adassay for updates.", false},
		{"word prefix", "Рекламация была отклонена поставщиком.", false},
		{"neutral prose", "The team compared three storage engines on the same hardware.", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fires(t, config.FeatureDisclaimer, core.Segment{Text: c.text}, Doc{}); got != c.want {
				t.Errorf("%q fired = %v, want %v", c.text, got, c.want)
			}
		})
	}
}

func TestDisclaimerSkipsMenus(t *testing.T) {
	seg := core.Segment{
		Text: "Редакция Реклама Контакты Вакансии",
		Links: []core.Link{
			{Href: "/info/", Text: "Редакция"}, {Href: "/sales", Text: "Реклама"},
			{Href: "/contacts", Text: "Контакты"}, {Href: "/career", Text: "Вакансии"},
		},
	}
	if fires(t, config.FeatureDisclaimer, seg, Doc{}) {
		t.Error("a footer menu fired disclaimer")
	}
}

func TestBrandDensity(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		links []core.Link
		doc   []string
		want  bool
	}{
		{name: "repeated name the article ignores", want: true,
			text: "Produced in partnership with ShieldPath, and ShieldPath paid for the placement."},
		{name: "repeated name next to a sponsored link", want: true,
			text:  "The ShieldPath plan covers five devices, and ShieldPath throws in a router licence.",
			links: []core.Link{{Href: "https://shieldpath.example/plans", Rel: "sponsored"}}},
		{name: "repeated name next to a promo code", want: true,
			text: "Readers get three months of ShieldPath with promo code SHIELD20, and ShieldPath renews at list price."},
		{name: "repeated name pushed with scarcity", want: true,
			text: "Claim your ShieldPath trial today only, because ShieldPath raises the price on Monday."},
		{name: "mentioned once", want: false,
			text: "Teams have been moving to TurboLane CI, which bills by the minute."},
		{name: "repeated but the article is about it", want: false,
			text: "The ShieldPath client leaks on reconnect, and ShieldPath knows it.",
			doc:  []string{"ShieldPath publishes an audit every year.", "Compare that to how ShieldPath handles DNS."}},
		{name: "technical term repeated in explanatory prose", want: false,
			text: "The Kubernetes control plane reconciles desired state, and every Kubernetes node runs a kubelet."},
		{name: "technical term repeated in a diagram", want: false,
			text: "Kubernetes Cluster │ Control Plane │ API Server │ Scheduler │ etcd │ Worker Node │ Kubernetes Pods"},
		{name: "technical term next to an ordinary link", want: false,
			text:  "Minikube starts a single-node cluster, and Minikube ships the dashboard addon.",
			links: []core.Link{{Href: "https://minikube.sigs.k8s.io/docs/start/"}}},
		{name: "sentence-initial word is not a brand", want: false,
			text: "Because it caches. Because it caches, the build is fast."},
		{name: "plain prose", want: false,
			text: "The compiler rewrites the loop into a single pass."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			segs := []core.Segment{{ID: "s1", Text: c.text, Links: c.links}}
			for i, extra := range c.doc {
				segs = append(segs, core.Segment{ID: fmt.Sprintf("s%d", i+2), Text: extra})
			}
			doc := NewDoc(segs)
			if got := fires(t, config.FeatureBrandDensity, segs[0], doc); got != c.want {
				t.Errorf("%q fired = %v, want %v", c.text, got, c.want)
			}
		})
	}
}

func TestCTAUrgency(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"both halves", "Use code TYPE20 at checkout for twenty percent off during launch week.", true},
		{"russian", "Успей оформить заказ, предложение только сегодня.", true},
		{"cta without urgency", "Readers can get three months free at checkout, and the plan renews afterwards.", false},
		{"urgency without cta", "The migration window ends soon, so plan the switch now.", false},
		{"neither", "Hot swap sockets are the feature to insist on.", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fires(t, config.FeatureCTAUrgency, core.Segment{Text: c.text}, Doc{}); got != c.want {
				t.Errorf("%q fired = %v, want %v", c.text, got, c.want)
			}
		})
	}
}

func TestDetectOrderAndIndependence(t *testing.T) {
	seg := core.Segment{
		Text:  "На правах рекламы: используйте промокод SAVE20. Успей купить ShopMax, ShopMax только сегодня со скидкой.",
		Links: []core.Link{{Href: "https://shop.example/x?tag=blog-20", Rel: "sponsored"}},
	}
	got := Detect(seg, Doc{}, patterns(t))
	want := config.Features
	if len(got) != len(want) {
		t.Fatalf("Detect = %v, want all of %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Detect[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if got := Detect(core.Segment{Text: "A plain paragraph about databases."}, Doc{}, patterns(t)); len(got) != 0 {
		t.Errorf("Detect on neutral text = %v, want none", got)
	}
}
