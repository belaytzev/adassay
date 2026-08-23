// Package rules is L2: deterministic features read off a segment and the
// scoring that turns them into a verdict.
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

// Detect returns the features a segment fires, in the canonical order of
// config.Features.
func Detect(seg core.Segment, doc Doc, p config.Patterns) []string {
	fired := map[string]bool{
		config.FeatureRelSponsored: relSponsored(seg.Links),
		config.FeaturePromoCode:    promoCode(seg.Text, p.PromoWords),
		config.FeatureAffiliate:    affiliate(seg.Links, p),
		config.FeatureDisclaimer:   matchesAny(seg.Text, p.Disclaimers),
		config.FeatureBrandDensity: brandDensity(seg.Text, doc),
		config.FeatureCTAUrgency:   matchesAny(seg.Text, p.CTAWords) && matchesAny(seg.Text, p.UrgencyWords),
	}
	var out []string
	for _, f := range config.Features {
		if fired[f] {
			out = append(out, f)
		}
	}
	return out
}

// Doc is the document a segment was cut from: how many of its segments mention
// each name. The fuzzy features are relative — a brand is only suspicious when
// the rest of the article ignores it. The zero Doc is a document of one
// segment, which is what checking a bare piece of text is.
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

// brandToken is a name as markup leaves it: a capitalised run, internal capitals
// kept, so NordVPN and TurboLane survive as one token.
var brandToken = regexp.MustCompile(`\p{Lu}[\p{L}\p{Nd}]{2,}`)

// brands counts name mentions in one text. A capital that opens a sentence is
// grammar rather than a name, and is not counted — otherwise every "Because"
// is a brand.
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

// brandDensity fires when a name is repeated inside one segment and the rest of
// the document does not mention it: the paragraph is about that product, the
// article is not.
func brandDensity(text string, doc Doc) bool {
	for b, n := range brands(text) {
		if n >= 2 && doc.brand[b] <= 1 {
			return true
		}
	}
	return false
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
// "реклама" fires on "рекламация" and "#ad" fires on "#adassay".
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
