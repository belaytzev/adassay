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

// Extract turns a page into segments. L1 runs on the raw markup at the same
// time: extraction is precisely what throws hidden nodes away, so the detector
// has to see the page before readability does.
func Extract(page []byte, pageURL string, cfg config.L1) (core.Result, error) {
	type l1 struct {
		findings []core.Finding
		err      error
	}
	done := make(chan l1, 1)
	go func() {
		f, err := Hidden(bytes.NewReader(page), cfg)
		done <- l1{f, err}
	}()

	var base *url.URL
	if pageURL != "" {
		base, _ = url.Parse(pageURL)
	}
	art, err := readability.FromReader(bytes.NewReader(page), base)
	hidden := <-done

	if err != nil {
		return core.Result{}, fmt.Errorf("extract: %w", err)
	}
	if hidden.err != nil {
		return core.Result{}, fmt.Errorf("extract: l1: %w", hidden.err)
	}

	segs := segment(art.Node)
	texts := make([]string, len(segs))
	for i, s := range segs {
		texts[i] = s.Text
	}
	return core.Result{
		Title:    art.Title,
		Text:     strings.Join(texts, "\n\n"),
		Segments: segs,
		Hidden:   hidden.findings,
		Domain:   NormalizeDomain(pageURL),
	}, nil
}

var blockTags = map[string]bool{
	"p": true, "li": true, "blockquote": true, "pre": true,
	"dd": true, "dt": true, "figcaption": true, "td": true,
}

func isBlock(tag string) bool { return blockTags[tag] }

func isHeading(tag string) bool {
	return len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6'
}

type segmenter struct {
	out     []core.Segment
	pending string
}

// segment splits extracted content into scoring units: a paragraph or a list
// item is one segment, and a heading rides on the block that follows it —
// alone it carries too little text to judge, but it frames what comes next.
func segment(root *html.Node) []core.Segment {
	if root == nil {
		return nil
	}
	s := &segmenter{}
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
			s.emit(text, links)
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		s.walk(c)
	}
}

func (s *segmenter) emit(text string, links []core.Link) {
	text = clean(text)
	if s.pending != "" {
		text = strings.TrimSpace(s.pending + "\n\n" + text)
		s.pending = ""
	}
	if text == "" {
		return
	}
	s.out = append(s.out, core.Segment{
		ID:    fmt.Sprintf("s%d", len(s.out)+1),
		Text:  text,
		Links: links,
	})
}

// blockContent reads a block's own text and links, stopping at nested blocks so
// that a list item inside a list item stays a segment of its own.
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
			case isBlock(c.Data), isHeading(c.Data), c.Data == "script", c.Data == "style", classify(c) != "":
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

// dropPadding removes invisible padding from text the agent will read, but keeps
// ZWNJ and ZWJ: they are spelling in Persian, Urdu and Hindi and are what holds
// an emoji sequence together. Stripping them here would corrupt the document the
// filter exists to hand over intact — the hash space strips them separately, in
// store.Normalize.
func dropPadding(r rune) rune {
	if r == 0x200C || r == 0x200D {
		return r
	}
	return dropInvisible(r)
}

// NormalizeDomain reduces a URL or a bare host to the key the domain score is
// kept under: punycode, no www, lower case. Subdomains stay — a blog platform
// and its user subdomains are not the same source.
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
