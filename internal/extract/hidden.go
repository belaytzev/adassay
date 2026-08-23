// Package extract turns a raw HTML page into filterable text. Hidden is the L1
// detector: it walks the raw DOM before extraction throws invisible nodes away,
// because text a source shows to parsers but not to humans is the cheapest
// deterministic signal on the page.
package extract

import (
	"io"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"

	"github.com/belaytzev/adfilter/internal/config"
	"github.com/belaytzev/adfilter/internal/core"
)

const (
	KindCSSHidden = "css_hidden"
	KindOffScreen = "offscreen"
	KindAria      = "aria_hidden"
	KindHiddenAtt = "hidden_attr"
	KindComment   = "comment"
	KindNoscript  = "noscript"
	KindTemplate  = "template"
	KindColor     = "color_on_color"
	KindLongAttr  = "long_attr"
	KindInvisible = "invisible_unicode"
)

const sampleLen = 300

// minWords is how many words a long attribute needs before it reads as text
// rather than as encoded data.
const minWords = 5

// offScreenPx is how far a coordinate has to be pushed out before it counts as
// hiding rather than layout.
const offScreenPx = -500

// zeroWidthRun is where invisible code points stop reading as typography —
// emoji joiners, word joiners, soft break hints — and start reading as a payload.
// It counts consecutive code points: Persian spelling puts one ZWNJ between two
// letters and an emoji family holds together on single ZWJs, so a per-node total
// would call ordinary text an injection.
const zeroWidthRun = 8

// Hidden reports nodes the page keeps out of sight. It reads static markup
// only: inline styles, attributes and node types.
//
// ponytail: static CSS only, headless render if misses show up
func Hidden(r io.Reader, cfg config.L1) ([]core.Finding, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}
	var out []core.Finding
	walk(doc, cfg, &out)
	return dedupe(out), nil
}

// dedupe collapses repeated findings: templated markup repeats the same hidden
// string on every widget of a page, and counting it ten times would inflate the
// domain score off a single piece of boilerplate.
func dedupe(in []core.Finding) []core.Finding {
	seen := make(map[core.Finding]bool, len(in))
	var out []core.Finding
	for _, f := range in {
		if seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

func walk(n *html.Node, cfg config.L1, out *[]core.Finding) {
	switch n.Type {
	case html.CommentNode:
		if s := collapse(n.Data); significant(s, KindComment, cfg) {
			*out = append(*out, core.Finding{Kind: KindComment, Sample: sample(s)})
		}
		return
	case html.TextNode:
		if s := invisible(n.Data); s != "" {
			*out = append(*out, core.Finding{Kind: KindInvisible, Sample: sample(s)})
		}
		return
	case html.ElementNode:
		switch n.Data {
		case "script", "style":
			return
		}
		if kind := classify(n); kind != "" {
			if s := nodeText(n); significant(s, kind, cfg) {
				*out = append(*out, core.Finding{Kind: kind, Sample: sample(s)})
				return
			}
		}
		*out = append(*out, longAttrs(n, cfg)...)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, cfg, out)
	}
}

func classify(n *html.Node) string {
	switch n.Data {
	case "noscript":
		return KindNoscript
	case "template":
		return KindTemplate
	}
	if strings.EqualFold(attr(n, "aria-hidden"), "true") {
		return KindAria
	}
	if _, ok := lookup(n, "hidden"); ok {
		return KindHiddenAtt
	}
	return styleKind(parseStyle(attr(n, "style")))
}

func styleKind(st map[string]string) string {
	switch {
	case st["display"] == "none",
		st["visibility"] == "hidden",
		isZero(st["opacity"]),
		isZeroLength(st["font-size"]):
		return KindCSSHidden
	}
	// Collapsed accordions and screen-reader offsets use max-height:0 and
	// negative margins legitimately, and L1 findings cost the domain trust: a
	// detector nothing in the corpus exercises only buys false positives.
	for _, prop := range []string{"left", "top", "right", "bottom", "text-indent"} {
		if v, ok := length(st[prop]); ok && v <= offScreenPx {
			return KindOffScreen
		}
	}
	if c, bg := normColor(st["color"]), background(st); c != "" && c == bg {
		return KindColor
	}
	return ""
}

// background reads the colour out of either the longhand or the shorthand: the
// shorthand is what most pages actually write, and its colour is the one token
// that parses as one.
func background(st map[string]string) string {
	if v := st["background-color"]; v != "" {
		return normColor(v)
	}
	for _, tok := range strings.Fields(st["background"]) {
		if strings.HasPrefix(tok, "#") || tok == "white" || tok == "black" {
			return normColor(tok)
		}
	}
	return ""
}

// normColor puts equivalent notations of the same colour into one spelling, so
// that color:#fff on background:#ffffff still compares equal. Only the handful
// of names a text-hiding trick reaches for is worth mapping; anything else
// compares as written, which is how this read before.
func normColor(v string) string {
	v = strings.TrimSpace(v)
	switch v {
	case "white":
		return "#ffffff"
	case "black":
		return "#000000"
	}
	if len(v) == 4 && v[0] == '#' {
		return string([]byte{'#', v[1], v[1], v[2], v[2], v[3], v[3]})
	}
	return v
}

func longAttrs(n *html.Node, cfg config.L1) []core.Finding {
	var out []core.Finding
	names := []string{"alt", "title"}
	if n.Data == "meta" {
		names = []string{"content"}
	}
	for _, name := range names {
		v := collapse(attr(n, name))
		if len([]rune(v)) > cfg.LongAttrLength && prose(v) && significant(v, KindLongAttr, cfg) {
			out = append(out, core.Finding{Kind: KindLongAttr, Sample: sample(v)})
		}
	}
	return out
}

// prose keeps machine payloads out of the attribute detector: citation
// metadata, encoded query strings and ids are long but carry no words, while an
// injection aimed at a reader is written as sentences.
func prose(s string) bool {
	return len(strings.Fields(s)) >= minWords
}

// parseStyle turns an inline style attribute into normalised declarations.
func parseStyle(s string) map[string]string {
	if s == "" {
		return nil
	}
	st := map[string]string{}
	for _, decl := range strings.Split(s, ";") {
		prop, val, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		// The priority flag is dropped before comparison: display:none
		// !important hides exactly as much as display:none, and leaving the
		// flag in the value turns every exact match below into a miss.
		if i := strings.IndexByte(val, '!'); i >= 0 {
			val = val[:i]
		}
		st[strings.ToLower(strings.TrimSpace(prop))] = strings.ToLower(strings.Join(strings.Fields(val), " "))
	}
	return st
}

func isZero(v string) bool {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	return err == nil && f == 0
}

func isZeroLength(v string) bool {
	f, ok := length(v)
	return ok && f == 0
}

// length parses a CSS length, ignoring its unit: the interesting cases here are
// zero and large negatives, and both read the same in px, em or rem.
func length(v string) (float64, bool) {
	v = strings.TrimSpace(v)
	end := 0
	for end < len(v) && (v[end] == '-' || v[end] == '+' || v[end] == '.' || (v[end] >= '0' && v[end] <= '9')) {
		end++
	}
	if end == 0 {
		return 0, false
	}
	f, err := strconv.ParseFloat(v[:end], 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

func attr(n *html.Node, name string) string {
	v, _ := lookup(n, name)
	return v
}

func lookup(n *html.Node, name string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == name {
			return strings.TrimSpace(a.Val), true
		}
	}
	return "", false
}

// nodeText collapses the visible text of a subtree. <noscript> content is raw
// markup when scripting is assumed on, so it is re-parsed instead of dumped.
func nodeText(n *html.Node) string {
	var b strings.Builder
	collect(n, &b)
	s := collapse(b.String())
	if n.Data == "noscript" && strings.Contains(s, "<") {
		if frag, err := html.Parse(strings.NewReader(s)); err == nil {
			var inner strings.Builder
			collect(frag, &inner)
			s = collapse(inner.String())
		}
	}
	return s
}

func collect(n *html.Node, b *strings.Builder) {
	if n.Type == html.TextNode {
		b.WriteString(n.Data)
		b.WriteString(" ")
		return
	}
	if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collect(c, b)
	}
}

func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// looseKinds hide text with a mechanism ordinary pages do not use for prose:
// display:none, an off-screen offset, colour on colour, a noscript fallback. A
// long readable string behind one of those is evidence by itself.
//
// Everywhere else hiding is routine authoring — CMS comments, prerendered
// templates, screen-reader strings, meta descriptions — and the corpus showed
// length alone firing on seven clean pages out of twenty-one. Those kinds count
// only when the text instructs a reader or addresses an agent.
var looseKinds = map[string]bool{
	KindCSSHidden: true,
	KindOffScreen: true,
	KindColor:     true,
	KindNoscript:  true,
}

func significant(s, kind string, cfg config.L1) bool {
	if !hasLetters(s) {
		return false
	}
	if looseKinds[kind] && len([]rune(s)) > cfg.MinLength {
		return true
	}
	return directed(s, cfg)
}

// directed reports text written at a reader rather than about the page.
func directed(s string, cfg config.L1) bool {
	low := strings.ToLower(s)
	for _, list := range [][]string{cfg.Imperatives, cfg.AgentNames} {
		for _, p := range list {
			if strings.Contains(low, strings.ToLower(p)) {
				return true
			}
		}
	}
	return false
}

// invisible reports text smuggled through code points that render as nothing.
// Unicode tag characters carry a full ASCII payload and are decoded back; a long
// run of zero-width characters carries no readable text but is never typography,
// so it is reported together with the text it was hidden in.
func invisible(s string) string {
	var payload strings.Builder
	zeros, longest := 0, 0
	for _, r := range s {
		switch {
		case r >= 0xE0000 && r <= 0xE007F:
			if r >= 0xE0020 && r <= 0xE007E {
				payload.WriteRune(r - 0xE0000)
			}
		case r == 0x200B, r == 0x200C, r == 0x200D, r == 0x2060, r == 0xFEFF:
			zeros++
			longest = max(longest, zeros)
		default:
			zeros = 0
		}
	}
	if p := collapse(payload.String()); hasLetters(p) {
		return p
	}
	if longest >= zeroWidthRun {
		return strconv.Itoa(longest) + " zero-width characters in: " + collapse(strings.Map(dropInvisible, s))
	}
	return ""
}

func dropInvisible(r rune) rune {
	if r >= 0xE0000 && r <= 0xE007F {
		return -1
	}
	switch r {
	case 0x200B, 0x200C, 0x200D, 0x2060, 0xFEFF:
		return -1
	}
	return r
}

func hasLetters(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func sample(s string) string {
	r := []rune(s)
	if len(r) <= sampleLen {
		return s
	}
	return string(r[:sampleLen]) + "…"
}
