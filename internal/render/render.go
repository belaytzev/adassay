package render

import (
	"encoding/json"
	"io"
	"strings"

	"adassay.com/internal/core"
)

const (
	markerOpen  = "[[adassay:flag "
	markerClose = "]]"
	markerEnd   = "[[/adassay:flag]]"
)

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

func Defuse(text string) string {
	text = strings.ReplaceAll(text, "[[adassay:", "[ [adassay:")
	return strings.ReplaceAll(text, "[[/adassay:", "[ [/adassay:")
}

func Sample(text string) string {
	return Defuse(strings.Join(strings.Fields(text), " "))
}

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

func safeReasons(reasons []string) []string {
	out := make([]string, 0, len(reasons))
	for _, r := range reasons {
		if core.ValidReason(r) {
			out = append(out, r)
		}
	}
	return out
}

func JSON(w io.Writer, r core.Result) error {

	r.Text = Markdown(r)
	r = Safe(r)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}
