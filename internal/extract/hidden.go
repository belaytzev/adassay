package extract

import (
	"io"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"

	"adassay.com/internal/config"
	"adassay.com/internal/core"
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

const minWords = 5

const offScreenPx = -500

const zeroWidthRun = 8

func Hidden(r io.Reader, cfg config.L1) ([]core.Finding, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}
	var out []core.Finding
	walk(doc, cfg, &out)
	return dedupe(out), nil
}

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
		isZeroLength(st["font-size"]),
		clipped(st["clip"]),
		inset(st["clip-path"]),
		st["overflow"] == "hidden" && (isZeroLength(st["height"]) || isZeroLength(st["max-height"]) ||
			isZeroLength(st["width"]) || isZeroLength(st["max-width"])):
		return KindCSSHidden
	}

	for _, prop := range []string{"left", "top", "right", "bottom", "text-indent"} {
		if v, ok := length(st[prop]); ok && v <= offScreenPx {
			return KindOffScreen
		}
	}
	if transparent(st["color"]) || transparent(st["-webkit-text-fill-color"]) {
		return KindColor
	}
	if c, bg := normColor(st["color"]), background(st); c != "" && c == bg {
		return KindColor
	}
	return ""
}

func transparent(v string) bool {
	if v == "transparent" {
		return true
	}
	fn, args, ok := strings.Cut(strings.TrimSuffix(v, ")"), "(")
	if !ok {
		return false
	}
	legacy := false
	switch fn {
	case "rgb", "rgba", "hsl", "hsla":
		legacy = true
	case "hwb", "lab", "lch", "oklab", "oklch":
	default:
		return false
	}
	// Three channels, then alpha after a slash or as the fourth comma-separated
	// legacy component. Anything else is invalid CSS and renders opaque.
	if channels, alpha, ok := strings.Cut(args, "/"); ok {
		channels, relative := origin(channels)
		return channels != "" && numbers(strings.Fields(channels), 3, relative) && zero(alpha)
	}
	if parts := strings.Split(args, ","); legacy && numbers(parts, 4, false) {
		return zero(parts[3])
	}
	return false
}

// Strips the "from <origin>" of relative colour syntax, reporting whether it
// was there; the origin is one token, or a function up to its parenthesis.
func origin(channels string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimLeft(channels, " "), "from ")
	if !ok {
		return channels, false
	}
	end := strings.IndexAny(rest, " (")
	if end < 0 {
		return "", true
	}
	if rest[end] == '(' {
		if end = strings.IndexByte(rest, ')'); end < 0 {
			return "", true
		}
	}
	return rest[end+1:], true
}

// A channel is a number, a percentage, an angle or none; in relative syntax
// it may also be a channel keyword.
func numbers(toks []string, n int, relative bool) bool {
	if len(toks) != n {
		return false
	}
	for _, t := range toks {
		t = strings.TrimSpace(t)
		if t == "none" || (relative && isWord(t)) {
			continue
		}
		for _, unit := range []string{"%", "deg", "grad", "rad", "turn"} {
			t = strings.TrimSuffix(t, unit)
		}
		if _, err := strconv.ParseFloat(t, 64); err != nil {
			return false
		}
	}
	return true
}

func isWord(t string) bool {
	for i := 0; i < len(t); i++ {
		if c := t[i]; c < 'a' || c > 'z' {
			return false
		}
	}
	return t != ""
}

func zero(tok string) bool {
	f, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(tok), "%"), 64)
	return err == nil && f == 0
}

// rect(top, right, bottom, left) shows nothing once right <= left or bottom <= top.
func clipped(v string) bool {
	v, ok := strings.CutPrefix(v, "rect(")
	if !ok {
		return false
	}
	parts := strings.FieldsFunc(strings.TrimSuffix(v, ")"), func(r rune) bool { return r == ',' || r == ' ' })
	if len(parts) != 4 {
		return false
	}
	var side [4]float64
	for i, p := range parts {
		f, ok := length(p)
		if !ok {
			return false
		}
		side[i] = f
	}
	return side[1] <= side[3] || side[2] <= side[0]
}

// ponytail: inset() with percentages only, circle/polygon if a corpus page uses them
func inset(v string) bool {
	v, ok := strings.CutPrefix(v, "inset(")
	if !ok {
		return false
	}
	v, _, _ = strings.Cut(strings.TrimSuffix(v, ")"), " round")
	parts := strings.Fields(v)
	if len(parts) == 0 || len(parts) > 4 {
		return false
	}
	order := [][4]int{{0, 0, 0, 0}, {0, 1, 0, 1}, {0, 1, 2, 1}, {0, 1, 2, 3}}[len(parts)-1]
	var side [4]float64
	for i, j := range order {
		f, ok := length(parts[j])
		if !ok || (f != 0 && !strings.HasSuffix(parts[j], "%")) {
			return false
		}
		side[i] = f
	}
	return side[0]+side[2] >= 100 || side[1]+side[3] >= 100
}

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

func prose(s string) bool {
	return len(strings.Fields(s)) >= minWords
}

func parseStyle(s string) map[string]string {
	if s == "" {
		return nil
	}
	st := map[string]string{}
	for _, decl := range declarations(s) {
		prop, val, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}

		if i := strings.IndexByte(val, '!'); i >= 0 {
			val = val[:i]
		}
		st[strings.ToLower(strings.TrimSpace(prop))] = strings.ToLower(strings.Join(strings.Fields(val), " "))
	}
	return st
}

// Splits a style attribute the way the CSS tokenizer would: a comment is a
// space, a string is opaque until its quote or a newline, an unquoted url()
// token is opaque until its parenthesis, an escape binds what it escapes,
// and only a ';' outside every block ends a declaration.
func declarations(s string) []string {
	var out []string
	var b strings.Builder
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"' || c == '\'':
			end := opaque(s, i+1, c)
			b.WriteString(s[i:end])
			i = end - 1
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				i = len(s)
			} else {
				i += 2 + end + 1
			}
			b.WriteByte(' ')
		case identStart(s, i):
			name, end := ident(s, i)
			if name == "url" && end < len(s) && s[end] == '(' && !quoted(s[end+1:]) {
				end = opaque(s, end+1, ')')
			}
			b.WriteString(s[i:end])
			i = end - 1
		case c == '(' || c == '[' || c == '{':
			depth++
			b.WriteByte(c)
		case c == ')' || c == ']' || c == '}':
			depth = max(depth-1, 0)
			b.WriteByte(c)
		case c == ';' && depth == 0:
			out = append(out, b.String())
			b.Reset()
		default:
			b.WriteByte(c)
		}
	}
	return append(out, b.String())
}

// Index one past the closing delimiter, honouring escapes. A newline ends a
// string without being consumed; the end of the input closes anything.
func opaque(s string, from int, close byte) int {
	for i := from; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case close:
			return i + 1
		case '\n', '\r', '\f':
			if close != ')' {
				return i
			}
		}
	}
	return len(s)
}

func quoted(s string) bool {
	s = strings.TrimLeft(s, " \t\n\r\f")
	return s != "" && (s[0] == '"' || s[0] == '\'')
}

func identStart(s string, i int) bool {
	c := s[i]
	if i > 0 && identByte(s[i-1]) {
		return false
	}
	if c == '-' {
		return i+1 < len(s) && (s[i+1] == '-' || nameStart(s[i+1]))
	}
	return nameStart(c)
}

func nameStart(c byte) bool {
	return c == '_' || c == '\\' || c >= 0x80 || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func identByte(c byte) bool {
	return c == '-' || (c >= '0' && c <= '9') || nameStart(c)
}

// The identifier at i, lower-cased with its escapes decoded, and the index
// past it. An escape is up to six hex digits plus one optional space, or
// any other single byte.
func ident(s string, i int) (string, int) {
	var name strings.Builder
	for i < len(s) {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s):
			j := i + 1
			for j < len(s) && j < i+7 && isHex(s[j]) {
				j++
			}
			if j > i+1 {
				if code, err := strconv.ParseUint(s[i+1:j], 16, 32); err == nil {
					name.WriteRune(rune(code))
				}
				if j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n') {
					j++
				}
			} else {
				name.WriteByte(s[j])
				j++
			}
			i = j
		case identByte(c):
			name.WriteByte(c)
			i++
		default:
			return strings.ToLower(name.String()), i
		}
	}
	return strings.ToLower(name.String()), i
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func isZero(v string) bool {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	return err == nil && f == 0
}

func isZeroLength(v string) bool {
	f, ok := length(v)
	return ok && f == 0
}

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
