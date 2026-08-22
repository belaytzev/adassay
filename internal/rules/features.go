// Package rules is L2: deterministic features read off a segment and the
// scoring that turns them into a verdict.
package rules

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/belaytzev/adfilter/internal/config"
	"github.com/belaytzev/adfilter/internal/core"
)

// Detect returns the features a segment fires, in the canonical order of
// config.Features.
func Detect(seg core.Segment, p config.Patterns) []string {
	fired := map[string]bool{
		config.FeatureRelSponsored: relSponsored(seg.Links),
		config.FeaturePromoCode:    promoCode(seg.Text, p.PromoWords),
		config.FeatureAffiliate:    affiliate(seg.Links, p),
		config.FeatureDisclaimer:   matchesAny(seg.Text, p.Disclaimers),
	}
	var out []string
	for _, f := range config.Features {
		if fired[f] {
			out = append(out, f)
		}
	}
	return out
}

func relSponsored(links []core.Link) bool {
	for _, l := range links {
		for _, tok := range strings.Fields(l.Rel) {
			if strings.EqualFold(tok, "sponsored") {
				return true
			}
		}
	}
	return false
}

// codeToken is what a promo code looks like once it is set apart from prose:
// an all-caps run of letters and digits. \b is not used — it is ASCII-only and
// would never fire on a Cyrillic code.
var codeToken = regexp.MustCompile(`[\p{Lu}\p{Nd}]{4,20}`)

// codeWindow is how far from the promo word the code may sit. Wide enough for
// "use code" plus a few words, narrow enough that an unrelated acronym further
// down the paragraph does not count.
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

// bounded is regexp \b applied to both ends of a match: a word character of
// the pattern may not continue into a word character of the text. Without it
// "реклама" fires on "рекламация" and "#ad" fires on "#adfilter".
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
