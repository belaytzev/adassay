// Package render turns a filtered Result into the two shapes the CLI and the
// MCP server hand to an agent: markdown with markers, or JSON.
package render

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/belaytzev/adfilter/internal/core"
)

// Marker delimiters. The agent must be able to tell a filter decision from the
// page's own text, so the marker is a sequence no prose produces by accident.
const (
	markerOpen  = "[[adfilter:flag "
	markerClose = "]]"
	markerEnd   = "[[/adfilter:flag]]"
)

// Markdown assembles the visible document: Keep passes through, Drop is cut,
// Flag stays wrapped in a marker carrying its reasons.
func Markdown(r core.Result) string {
	var parts []string
	for _, s := range r.Segments {
		switch s.Verdict {
		case core.Keep:
			parts = append(parts, s.Text)
		case core.Flag:
			parts = append(parts, marker(s)+"\n"+s.Text+"\n"+markerEnd)
		case core.Drop:
		}
	}
	return strings.Join(parts, "\n\n")
}

func marker(s core.Segment) string {
	meta, err := json.Marshal(struct {
		ID      string   `json:"id"`
		Score   float64  `json:"score"`
		Reasons []string `json:"reasons,omitempty"`
	}{s.ID, s.Score, s.Reasons})
	if err != nil {
		meta = []byte("{}")
	}
	return markerOpen + string(meta) + markerClose
}

// JSON is the only serializer of Result: the CLI flag and the MCP tool both go
// through here, so their output can never drift apart.
func JSON(w io.Writer, r core.Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}
