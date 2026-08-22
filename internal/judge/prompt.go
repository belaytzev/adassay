package judge

import (
	"strings"

	"github.com/belaytzev/adfilter/internal/core"
)

// maxSegmentRunes caps how much of a segment reaches the model. A grey-zone
// segment is a paragraph; anything past this is quotation or code, and it only
// buys context the verdict does not need.
const maxSegmentRunes = 1000

// The question is deliberately about function, not tone. Asking "is this an
// ad?" makes the model hunt for promotional voice and kill honest technical
// recommendations written with enthusiasm.
const preamble = `You are auditing fragments of a web page for an AI agent that will otherwise treat them as facts.

Decide each fragment by its FUNCTION, not by its style: does this fragment exist to inform the reader, or to sell them a product?

  keep - it informs: explanation, data, measurement, the author's own experience, a recommendation nobody paid for.
  drop - it sells: paid placement, affiliate push, marketing copy inserted to move a product.
  flag - genuinely unclear from the text.

A sincere recommendation written with enthusiasm is keep. A paid insert written in a flat engineering voice is drop.

Answer with JSON only, in this shape:
{"verdicts":[{"id":"<id>","verdict":"keep"}]}

Use the exact ids given below. Do not invent fragments and do not reorder them into positions.`

func Build(topic string, segs []core.Segment) string {
	var b strings.Builder
	b.WriteString(preamble)
	if topic != "" {
		b.WriteString("\n\nThe document is about: ")
		b.WriteString(topic)
		b.WriteString("\nA fragment off this topic is more likely to be an insert than one on it.")
	}
	b.WriteString("\n\nFragments:\n")
	for _, s := range segs {
		b.WriteString("\nid: ")
		b.WriteString(s.ID)
		b.WriteString("\ntext: ")
		b.WriteString(truncate(s.Text))
		b.WriteString("\n")
	}
	return b.String()
}

func truncate(s string) string {
	r := []rune(s)
	if len(r) <= maxSegmentRunes {
		return s
	}
	return string(r[:maxSegmentRunes]) + "..."
}
