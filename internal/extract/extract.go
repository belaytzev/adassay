package extract

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"

	"github.com/go-shiori/go-readability"
	"golang.org/x/net/html"
	"golang.org/x/net/idna"

	"adassay.com/internal/config"
	"adassay.com/internal/core"
)

func Extract(page []byte, pageURL string, cfg config.L1) (core.Result, error) {
	type l1 struct {
		findings []core.Finding
		links    map[string][]core.Link
		blocks   []block
		visible  int
		err      error
	}
	done := make(chan l1, 1)
	go func() {
		doc, err := html.Parse(bytes.NewReader(page))
		if err != nil {
			done <- l1{err: err}
			return
		}
		var findings []core.Finding
		walk(doc, cfg, &findings)
		links, blocks, visible := rawScan(doc)
		done <- l1{findings: dedupe(findings), links: links, blocks: blocks, visible: visible}
	}()

	var base *url.URL
	if pageURL != "" {
		base, _ = url.Parse(pageURL)
	}
	art, err := readability.FromReader(bytes.NewReader(page), base)
	raw := <-done

	if err != nil {
		return core.Result{}, fmt.Errorf("extract: %w", err)
	}
	if raw.err != nil {
		return core.Result{}, fmt.Errorf("extract: l1: %w", raw.err)
	}

	segs := splice(segment(art.Node, raw.links), raw.blocks)
	texts := make([]string, len(segs))
	for i, s := range segs {
		texts[i] = s.Text
	}
	text := strings.Join(texts, "\n\n")
	return core.Result{
		Title:    art.Title,
		Text:     text,
		Segments: segs,
		Hidden:   raw.findings,
		Domain:   NormalizeDomain(pageURL),
		Visible:  raw.visible,
		Thin:     thin(page, text, raw.visible),
	}, nil
}

const (
	minVisible = 500
	thinFactor = 4
	shellBytes = 4096
)

// Kilobytes of markup showing almost no text is a JavaScript shell, however
// faithfully the little it shows was extracted.
func thin(page []byte, text string, visible int) bool {
	if visible < minVisible {
		return len(page) >= shellBytes
	}
	return thinFactor*len([]rune(text)) < visible
}

type block struct {
	text  string
	links []core.Link
}

func rawScan(root *html.Node) (map[string][]core.Link, []block, int) {
	idx := map[string][]core.Link{}
	var blocks []block
	visible := 0
	var rec func(*html.Node)
	rec = func(n *html.Node) {
		if n.Type == html.TextNode {
			visible += len([]rune(collapse(n.Data)))
			return
		}
		if n.Type == html.ElementNode {
			switch {
			case n.Data == "script" || n.Data == "style", classify(n) != "":
				return
			case isHeading(n.Data):
				_, links := blockContent(n)
				text := clean(nodeText(n))
				index(idx, text, links)
				blocks = append(blocks, block{text, links})
			case isBlock(n.Data), leaf(n):
				raw, links := blockContent(n)
				text := clean(raw)
				index(idx, text, links)
				blocks = append(blocks, block{text, links})
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(root)
	return idx, blocks, visible
}

func splice(segs []core.Segment, blocks []block) []core.Segment {
	at := map[string]int{}
	for i, s := range segs {
		for line := range strings.SplitSeq(s.Text, "\n\n") {
			if _, ok := at[line]; !ok {
				at[line] = i
			}
		}
	}
	last := -1
	for i, b := range blocks {
		if _, ok := at[b.text]; ok {
			last = i
		}
	}
	extra := map[int][]block{}
	anchor, n := -1, 0
	for i, b := range blocks {
		if i > last {
			break
		}
		if a, ok := at[b.text]; ok {
			anchor = a
			continue
		}
		if anchor < 0 || b.text == "" || !core.Sponsored(b.links) {
			continue
		}
		extra[anchor] = append(extra[anchor], b)
		n++
	}
	if n == 0 {
		return segs
	}
	out := make([]core.Segment, 0, len(segs)+n)
	for i, s := range segs {
		out = append(out, s)
		for _, b := range extra[i] {
			out = append(out, core.Segment{Text: b.text, Links: dedupeLinks(b.links)})
		}
	}
	for i := range out {
		out[i].ID = fmt.Sprintf("s%d", i+1)
	}
	return out
}

func index(idx map[string][]core.Link, key string, links []core.Link) {
	if key == "" || len(links) == 0 {
		return
	}
	idx[key] = dedupeLinks(append(idx[key], links...))
}

func dedupeLinks(links []core.Link) []core.Link {
	seen := make(map[core.Link]bool, len(links))
	// Grown, not preallocated: a block of a hundred thousand identical links
	// dedupes to one element that would otherwise hold the whole backing array
	// alive, invisible to anything measuring the result by length.
	var out []core.Link
	for _, l := range links {
		if seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	return out
}

var blockTags = map[string]bool{
	"p": true, "li": true, "blockquote": true, "pre": true,
	"dd": true, "dt": true, "figcaption": true, "td": true,
}

func isBlock(tag string) bool { return blockTags[tag] }

func leaf(n *html.Node) bool {
	if n.Type != html.ElementNode || n.Data != "div" {
		return false
	}
	var nested func(*html.Node) bool
	nested = func(p *html.Node) bool {
		for c := p.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode &&
				(isBlock(c.Data) || isHeading(c.Data) || c.Data == "div" || nested(c)) {
				return true
			}
		}
		return false
	}
	return !nested(n)
}

func isHeading(tag string) bool {
	return len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6'
}

type segmenter struct {
	out     []core.Segment
	pending string
	raw     map[string][]core.Link
}

func segment(root *html.Node, raw map[string][]core.Link) []core.Segment {
	if root == nil {
		return nil
	}
	s := &segmenter{raw: raw}
	s.walk(root)
	s.emit("", nil)
	return s.out
}

func (s *segmenter) walk(n *html.Node) {
	if n.Type == html.ElementNode {
		switch {
		case n.Data == "script" || n.Data == "style", classify(n) != "":
			return
		case isHeading(n.Data):
			s.emit("", nil)
			s.pending = clean(nodeText(n))
			return
		case isBlock(n.Data):
			text, links := blockContent(n)
			s.emit(text, s.resolve(clean(text), links))
		case leaf(n):
			raw, links := blockContent(n)
			text := clean(raw)
			s.push(text, s.resolve(text, links))
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		s.walk(c)
	}
}

func (s *segmenter) resolve(key string, own []core.Link) []core.Link {
	if raw := s.raw[key]; len(raw) > 0 {
		return raw
	}
	return own
}

func (s *segmenter) emit(text string, links []core.Link) {
	text = clean(text)
	if s.pending != "" {
		text = strings.TrimSpace(s.pending + "\n\n" + text)
		s.pending = ""
	}
	s.push(text, links)
}

func (s *segmenter) push(text string, links []core.Link) {
	if text == "" {
		return
	}
	s.out = append(s.out, core.Segment{
		ID:    fmt.Sprintf("s%d", len(s.out)+1),
		Text:  text,
		Links: dedupeLinks(links),
	})
}

func blockContent(n *html.Node) (string, []core.Link) {
	var b strings.Builder
	var links []core.Link
	var rec func(*html.Node)
	rec = func(parent *html.Node) {
		for c := parent.FirstChild; c != nil; c = c.NextSibling {
			switch {
			case c.Type == html.TextNode:
				b.WriteString(c.Data)
				b.WriteString(" ")
			case c.Type != html.ElementNode:
			case isBlock(c.Data), isHeading(c.Data), leaf(c), c.Data == "script", c.Data == "style", classify(c) != "":
			default:
				if c.Data == "a" {
					if href := attr(c, "href"); href != "" {
						links = append(links, core.Link{
							Href: href,
							Rel:  attr(c, "rel"),
							Text: clean(nodeText(c)),
						})
					}
				}
				rec(c)
			}
		}
	}
	rec(n)
	return b.String(), links
}

func clean(s string) string {
	return collapse(strings.Map(dropPadding, s))
}

func dropPadding(r rune) rune {
	if r == 0x200C || r == 0x200D {
		return r
	}
	return dropInvisible(r)
}

func NormalizeDomain(s string) string {
	host := strings.TrimSpace(s)
	if u, err := url.Parse(host); err == nil && u.Host != "" {
		host = u.Hostname()
	} else {
		host, _, _ = strings.Cut(host, "/")
		if h, _, ok := strings.Cut(host, ":"); ok {
			host = h
		}
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if ascii, err := idna.ToASCII(host); err == nil {
		host = ascii
	}
	return strings.TrimPrefix(host, "www.")
}
