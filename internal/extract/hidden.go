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
)

const sampleLen = 300

// offScreenPx is how far a coordinate has to be pushed out before it counts as
// hiding rather than layout.
const offScreenPx = -500

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
	return out, nil
}

func walk(n *html.Node, cfg config.L1, out *[]core.Finding) {
	switch n.Type {
	case html.CommentNode:
		if s := collapse(n.Data); hasLetters(s) {
			*out = append(*out, core.Finding{Kind: KindComment, Sample: sample(s)})
		}
		return
	case html.ElementNode:
		switch n.Data {
		case "script", "style":
			return
		}
		if kind := classify(n); kind != "" {
			if s := nodeText(n); hasLetters(s) {
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
		isZeroLength(st["font-size"]),
		isZeroLength(st["max-height"]) && st["overflow"] == "hidden":
		return KindCSSHidden
	}
	for _, prop := range []string{"left", "top", "right", "margin-left", "margin-top", "text-indent"} {
		if v, ok := length(st[prop]); ok && v <= offScreenPx {
			return KindOffScreen
		}
	}
	if c, bg := st["color"], st["background-color"]; c != "" && c == bg {
		return KindColor
	}
	return ""
}

func longAttrs(n *html.Node, cfg config.L1) []core.Finding {
	var out []core.Finding
	names := []string{"alt", "title"}
	if n.Data == "meta" {
		names = []string{"content"}
	}
	for _, name := range names {
		v := collapse(attr(n, name))
		if len([]rune(v)) > cfg.LongAttrLength {
			out = append(out, core.Finding{Kind: KindLongAttr, Sample: sample(v)})
		}
	}
	return out
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
