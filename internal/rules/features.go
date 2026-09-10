package rules

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"adassay.com/internal/config"
	"adassay.com/internal/core"
)

func Detect(seg core.Segment, doc Doc, p config.Patterns) []string {
	fired := map[string]bool{
		config.FeatureRelSponsored: core.Sponsored(seg.Links),
		config.FeaturePromoCode:    promoCode(seg.Text, p.PromoWords),
		config.FeatureAffiliate:    affiliate(seg.Links, p),
		config.FeatureDisclaimer:   matchesAny(seg.Text, p.Disclaimers) && !label(seg.Text, p.Disclaimers) && !menu(seg),
		config.FeatureCTAUrgency:   matchesAny(seg.Text, p.CTAWords) && matchesAny(seg.Text, p.UrgencyWords),
	}
	fired[config.FeatureBrandDensity] = corroborated(fired) && brandDensity(seg.Text, doc)
	var out []string
	for _, f := range config.Features {
		if fired[f] {
			out = append(out, f)
		}
	}
	return out
}

type Doc struct {
	brand map[string]int
}

func NewDoc(segs []core.Segment) Doc {
	d := Doc{brand: make(map[string]int)}
	for _, s := range segs {
		for b := range brands(s.Text) {
			d.brand[b]++
		}
	}
	return d
}

var brandToken = regexp.MustCompile(`\p{Lu}[\p{L}\p{Nd}]{2,}`)

func brands(text string) map[string]int {
	out := map[string]int{}
	for _, m := range brandToken.FindAllStringIndex(text, -1) {
		if !bounded(text, m[0], m[1]-m[0]) || sentenceStart(text, m[0]) {
			continue
		}
		out[text[m[0]:m[1]]]++
	}
	return out
}

func sentenceStart(s string, i int) bool {
	for j := i; j > 0; {
		r, n := utf8.DecodeLastRuneInString(s[:j])
		j -= n
		if unicode.IsSpace(r) {
			continue
		}
		return r == '.' || r == '!' || r == '?' || r == ':'
	}
	return true
}

func corroborated(fired map[string]bool) bool {
	for _, ok := range fired {
		if ok {
			return true
		}
	}
	return false
}

func brandDensity(text string, doc Doc) bool {
	for b, n := range brands(text) {
		if n >= 2 && doc.brand[b] <= 1 {
			return true
		}
	}
	return false
}

var codeToken = regexp.MustCompile(`[\p{Lu}\p{Nd}]{4,20}`)

const codeWindow = 60

func promoCode(text string, words []string) bool {
	for _, w := range words {
		for i := 0; i+len(w) <= len(text); i++ {
			if !strings.EqualFold(text[i:i+len(w)], w) || !bounded(text, i, len(w)) {
				continue
			}
			if hasCode(window(text, i, len(w))) {
				return true
			}
		}
	}
	return false
}

func hasCode(s string) bool {
	for _, m := range codeToken.FindAllStringIndex(s, -1) {
		tok := s[m[0]:m[1]]
		if strings.IndexFunc(tok, unicode.IsLetter) >= 0 && bounded(s, m[0], m[1]-m[0]) {
			return true
		}
	}
	return false
}

func window(s string, i, n int) string {
	lo, hi := i-codeWindow, i+n+codeWindow
	if lo < 0 {
		lo = 0
	}
	if hi > len(s) {
		hi = len(s)
	}
	return s[lo:hi]
}

func affiliate(links []core.Link, p config.Patterns) bool {
	for _, l := range links {
		u, err := url.Parse(l.Href)
		if err != nil {
			continue
		}
		host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
		for _, h := range p.AffiliateHosts {
			h = strings.ToLower(h)
			if host == h || strings.HasSuffix(host, "."+h) {
				return true
			}
		}
		params := map[string]bool{}
		for k := range u.Query() {
			params[strings.ToLower(k)] = true
		}
		for _, k := range p.AffiliateParams {
			if params[strings.ToLower(k)] {
				return true
			}
		}
	}
	return false
}

// A segment that is nothing but the marker is a slot label or a menu entry;
// the disclosure worth acting on sits in prose.
func label(text string, patterns []string) bool {
	bare := strings.TrimFunc(text, func(r rune) bool { return !isWord(r) })
	for _, p := range patterns {
		if strings.EqualFold(bare, strings.TrimFunc(p, func(r rune) bool { return !isWord(r) })) {
			return true
		}
	}
	return false
}

const menuShare = 0.7

func menu(seg core.Segment) bool {
	linked := 0
	for _, l := range seg.Links {
		linked += len([]rune(l.Text))
	}
	total := len([]rune(seg.Text))
	return total > 0 && float64(linked) >= menuShare*float64(total)
}

func matchesAny(text string, patterns []string) bool {
	for _, p := range patterns {
		for i := 0; i+len(p) <= len(text); i++ {
			if strings.EqualFold(text[i:i+len(p)], p) && bounded(text, i, len(p)) {
				return true
			}
		}
	}
	return false
}

func bounded(s string, i, n int) bool {
	first, _ := utf8.DecodeRuneInString(s[i:])
	last, _ := utf8.DecodeLastRuneInString(s[:i+n])
	if isWord(first) && i > 0 {
		if r, _ := utf8.DecodeLastRuneInString(s[:i]); isWord(r) {
			return false
		}
	}
	if isWord(last) && i+n < len(s) {
		if r, _ := utf8.DecodeRuneInString(s[i+n:]); isWord(r) {
			return false
		}
	}
	return true
}

func isWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }
