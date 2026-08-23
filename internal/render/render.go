// Package render turns a filtered Result into the two shapes the CLI and the
// MCP server hand to an agent: markdown with markers, or JSON.
package render

import (
	"encoding/json"
	"io"
	"strings"

	"adassay.com/internal/core"
)

// Marker delimiters. The agent must be able to tell a filter decision from the
// page's own text, so the marker is a sequence no prose produces by accident.
const (
	markerOpen  = "[[adassay:flag "
	markerClose = "]]"
	markerEnd   = "[[/adassay:flag]]"
)

// Markdown assembles the visible document: Keep passes through, Drop is cut,
// Flag stays wrapped in a marker carrying its reasons.
func Markdown(r core.Result) string {
	var parts []string
	for _, s := range r.Segments {
		switch s.Verdict {
		case core.Keep:
			parts = append(parts, Defuse(s.Text))
		case core.Flag:
			parts = append(parts, marker(s)+"\n"+Defuse(s.Text)+"\n"+markerEnd)
		case core.Drop:
		}
	}
	return strings.Join(parts, "\n\n")
}

// Defuse breaks marker syntax the page wrote itself. Without it a paragraph
// containing the closing marker ends its own annotation early, and one
// containing an opening marker forges a verdict the filter never reached —
// the whole point of the markers is that only the filter can write them.
// Anything printed alongside the document goes through here too.
// Each token is broken on its own rather than by splitting every "[[": that
// pass leaves "[[[adassay:" a live marker, because replacement is
// non-overlapping and the third bracket re-pairs with the one it wrote.
func Defuse(text string) string {
	text = strings.ReplaceAll(text, "[[adassay:", "[ [adassay:")
	return strings.ReplaceAll(text, "[[/adassay:", "[ [/adassay:")
}

// Sample prepares page-controlled text for printing beside the document: one
// line, marker syntax broken. A hidden-text sample is the injection itself.
func Sample(text string) string {
	return Defuse(strings.Join(strings.Fields(text), " "))
}

// Safe copies r with every page-controlled string defused. An agent scans the
// whole payload it gets back for markers, not only the rendered document, so
// anything handed over as structured data goes through here first.
func Safe(r core.Result) core.Result {
	r.Title = Sample(r.Title)
	r.Segments = append([]core.Segment(nil), r.Segments...)
	for i := range r.Segments {
		r.Segments[i].Text = Defuse(r.Segments[i].Text)
		r.Segments[i].Reasons = safeReasons(r.Segments[i].Reasons)
		r.Segments[i].Links = safeLinks(r.Segments[i].Links)
	}
	r.Hidden = append([]core.Finding(nil), r.Hidden...)
	for i := range r.Hidden {
		r.Hidden[i].Sample = Sample(r.Hidden[i].Sample)
	}
	return r
}

// safeLinks defuses the markup a segment was built from. href, rel and the
// anchor text are page-controlled and serialized alongside the document: an
// agent scanning the payload for markers would find them there just as readily
// as in the text.
func safeLinks(links []core.Link) []core.Link {
	if len(links) == 0 {
		return links
	}
	out := make([]core.Link, len(links))
	for i, l := range links {
		out[i] = core.Link{Href: Sample(l.Href), Rel: Sample(l.Rel), Text: Sample(l.Text)}
	}
	return out
}

func marker(s core.Segment) string {
	meta, err := json.Marshal(struct {
		ID      string   `json:"id"`
		Score   float64  `json:"score"`
		Reasons []string `json:"reasons,omitempty"`
	}{s.ID, s.Score, safeReasons(s.Reasons)})
	if err != nil {
		meta = []byte("{}")
	}
	return markerOpen + string(meta) + markerClose
}

// safeReasons keeps only rule identifiers. A reason can come from the shared
// database, which is somebody else's server, and encoding/json does not escape
// "]": one reason carrying "]]" closes the marker early and the rest of it
// lands in the document as text the agent reads as the page's own.
func safeReasons(reasons []string) []string {
	out := make([]string, 0, len(reasons))
	for _, r := range reasons {
		if core.ValidReason(r) {
			out = append(out, r)
		}
	}
	return out
}

// JSON is the only serializer of Result: the CLI flag and the MCP tool both go
// through here, so their output can never drift apart.
func JSON(w io.Writer, r core.Result) error {
	// Text is the filtered document, not the page as extracted: an agent that
	// reads this field instead of walking the segments must not get back the
	// paragraphs the run decided to cut.
	r.Text = Markdown(r)
	r = Safe(r)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}
